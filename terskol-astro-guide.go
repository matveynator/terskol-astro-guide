package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	webview "github.com/jchv/go-webview-selector"
)

// =============================
// Embedded static assets.
// =============================

//go:embed static/*
var staticFiles embed.FS

var (
	portFlag                   = flag.Int("port", 8765, "web server port")
	directoryFlag              = flag.String("directory", ".", "directory to serve files from")
	dioOutputPathTemplateFlag  = flag.String("dio-output-path-template", "", "explicit DIO output file path template with %d placeholder")
	dioInputPathTemplateFlag   = flag.String("dio-input-path-template", "", "explicit DIO input file path template with %d placeholder")
	dioLinuxOutputPathTemplate = flag.String("dio-linux-output-path-template", "/sys/class/gpio/gpio%d/value", "Linux DIO output file path template")
	dioLinuxInputPathTemplate  = flag.String("dio-linux-input-path-template", "/sys/class/gpio/gpio%d/value", "Linux DIO input file path template")
	dioWindowsOutputPathFlag   = flag.String("dio-windows-output-path-template", `C:\Vecow\ECX1K\do%d.value`, "Windows DIO output file path template")
	dioWindowsInputPathFlag    = flag.String("dio-windows-input-path-template", `C:\Vecow\ECX1K\di%d.value`, "Windows DIO input file path template")
	labelsFileFlag             = flag.String("labels-file", "dio-labels.json", "path to labels file")
	inputOnVoltageFlag         = flag.Float64("input-on-voltage", 24.0, "voltage value used when DI source is digital and signal is active")
	inputOffVoltageFlag        = flag.Float64("input-off-voltage", 0.0, "voltage value used when DI source is digital and signal is inactive")
	inputThresholdVoltageFlag  = flag.Float64("input-threshold-voltage", 2.0, "threshold used to map numeric DI voltage to on/off signal")
)

const (
	inputCount           = 8
	outputCount          = 8
	serverStartupTimeout = 45 * time.Second
)

// =============================
// Domain model.
// =============================

type inputState struct {
	Channel int    `json:"channel"`
	Signal  string `json:"signal"`
	Voltage string `json:"voltage"`
	Hz      string `json:"hz"`
	Label   string `json:"label"`
}

type outputState struct {
	Channel int    `json:"channel"`
	Power   string `json:"power"`
	PWM     int    `json:"pwm"`
	Label   string `json:"label"`
}

type appState struct {
	Inputs  []inputState  `json:"inputs"`
	Outputs []outputState `json:"outputs"`
}

type setOutputPowerRequest struct {
	Channel int    `json:"channel"`
	Power   string `json:"power"`
}

type setOutputPWMRequest struct {
	Channel int `json:"channel"`
	PWM     int `json:"pwm"`
}

type setLabelRequest struct {
	Kind    string `json:"kind"`
	Channel int    `json:"channel"`
	Label   string `json:"label"`
}

type ioPaths struct {
	inputTemplate  string
	outputTemplate string
}

type runtimeConfig struct {
	inputOnVoltage        float64
	inputOffVoltage       float64
	inputThresholdVoltage float64
}

type inputMetric struct {
	lastSignal string
	lastEdgeAt time.Time
	lastHz     float64
}

type stateCommand struct {
	kind    string
	channel int
	power   string
	pwm     int
	label   string
	target  string
	reply   chan stateReply
}

type stateReply struct {
	state appState
	err   error
}

// =============================
// Main entry point.
// =============================

func main() {
	flag.Parse()

	resolvedIOPaths := ioPaths{
		inputTemplate:  resolvePathTemplate(*dioInputPathTemplateFlag, *dioLinuxInputPathTemplate, *dioWindowsInputPathFlag),
		outputTemplate: resolvePathTemplate(*dioOutputPathTemplateFlag, *dioLinuxOutputPathTemplate, *dioWindowsOutputPathFlag),
	}

	stateCommands := make(chan stateCommand)
	go runStateOwner(
		stateCommands,
		resolvedIOPaths,
		runtimeConfig{
			inputOnVoltage:        *inputOnVoltageFlag,
			inputOffVoltage:       *inputOffVoltageFlag,
			inputThresholdVoltage: *inputThresholdVoltageFlag,
		},
		*labelsFileFlag,
	)

	http.HandleFunc("/api/state", handleGetState(stateCommands))
	http.HandleFunc("/api/output/power", handleSetOutputPower(stateCommands))
	http.HandleFunc("/api/output/pwm", handleSetOutputPWM(stateCommands))
	http.HandleFunc("/api/label", handleSetLabel(stateCommands))
	http.HandleFunc("/", handleRequest)

	address := fmt.Sprintf("127.0.0.1:%d", *portFlag)
	log.Printf("startup: starting HTTP server on http://%s", address)

	httpListener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("startup: listen failed: %v", err)
	}

	go func() {
		err := http.Serve(httpListener, nil)
		if err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("shutdown: HTTP server stopped: %v", err)
		}
	}()

	waitForServerReadiness(address, serverStartupTimeout)

	window := webview.New(false)
	if window == nil {
		log.Fatal("webview: failed to create window")
	}
	defer window.Destroy()

	window.SetTitle("DIO/DO Control · ECX-1000-2G")
	window.SetSize(1120, 760, webview.HintNone)
	window.Navigate("http://" + address)

	log.Printf("webview: window started")
	window.Run()
	log.Printf("shutdown: webview stopped")
}

func resolvePathTemplate(explicitTemplate string, linuxTemplate string, windowsTemplate string) string {
	trimmedExplicitTemplate := strings.TrimSpace(explicitTemplate)
	if trimmedExplicitTemplate != "" {
		log.Printf("dio: using explicit template=%s", trimmedExplicitTemplate)
		return trimmedExplicitTemplate
	}

	if runtime.GOOS == "windows" {
		return windowsTemplate
	}

	return linuxTemplate
}

func waitForServerReadiness(address string, timeout time.Duration) {
	httpClient := http.Client{Timeout: 600 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	probeURL := "http://" + address + "/api/state"

	for {
		response, err := httpClient.Get(probeURL)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				log.Printf("startup: HTTP server ready at %s", probeURL)
				return
			}
		}

		if time.Now().After(deadline) {
			log.Printf("startup: continue without readiness after timeout=%s", timeout)
			return
		}

		time.Sleep(250 * time.Millisecond)
	}
}

// =============================
// State owner goroutine.
// =============================

func runStateOwner(stateCommands <-chan stateCommand, resolvedIOPaths ioPaths, config runtimeConfig, labelsFile string) {
	state := buildInitialState(loadLabels(labelsFile))
	inputMetrics := buildInitialInputMetrics()
	refreshInputSignals(&state, inputMetrics, resolvedIOPaths.inputTemplate, config, time.Now())

	for command := range stateCommands {
		switch command.kind {
		case "get":
			refreshInputSignals(&state, inputMetrics, resolvedIOPaths.inputTemplate, config, time.Now())
			command.reply <- stateReply{state: cloneState(state)}

		case "set_output_power":
			nextState, err := applyOutputPower(state, command.channel, command.power, resolvedIOPaths.outputTemplate)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		case "set_output_pwm":
			nextState, err := applyOutputPWM(state, command.channel, command.pwm, resolvedIOPaths.outputTemplate)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		case "set_label":
			nextState, err := applyLabel(state, command.target, command.channel, command.label)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveLabels(labelsFile, nextState); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		default:
			command.reply <- stateReply{state: cloneState(state), err: errors.New("unknown command")}
		}
	}
}

func buildInitialState(savedLabels map[string]string) appState {
	inputs := make([]inputState, 0, inputCount)
	for channelIndex := 1; channelIndex <= inputCount; channelIndex++ {
		labelKey := "input-" + strconv.Itoa(channelIndex)
		label := strings.TrimSpace(savedLabels[labelKey])
		if label == "" {
			label = fmt.Sprintf("DI %d", channelIndex-1)
		}

		inputs = append(inputs, inputState{Channel: channelIndex, Signal: "off", Voltage: "0.0V", Hz: "0.00 Hz", Label: label})
	}

	outputs := make([]outputState, 0, outputCount)
	for channelIndex := 1; channelIndex <= outputCount; channelIndex++ {
		labelKey := "output-" + strconv.Itoa(channelIndex)
		label := strings.TrimSpace(savedLabels[labelKey])
		if label == "" {
			label = fmt.Sprintf("DO %d", channelIndex-1)
		}

		outputs = append(outputs, outputState{Channel: channelIndex, Power: "off", PWM: 0, Label: label})
	}

	return appState{Inputs: inputs, Outputs: outputs}
}

func applyOutputPower(state appState, channel int, nextPower string, outputPathTemplate string) (appState, error) {
	if channel < 1 || channel > outputCount {
		return state, errors.New("invalid output channel")
	}
	if nextPower != "on" && nextPower != "off" {
		return state, errors.New("power must be on or off")
	}

	if err := writeOutputPower(channel, nextPower, outputPathTemplate); err != nil {
		return state, err
	}

	nextState := cloneState(state)
	nextState.Outputs[channel-1].Power = nextPower
	if nextPower == "off" {
		nextState.Outputs[channel-1].PWM = 0
	}

	return nextState, nil
}

func applyOutputPWM(state appState, channel int, nextPWM int, outputPathTemplate string) (appState, error) {
	if channel < 1 || channel > outputCount {
		return state, errors.New("invalid output channel")
	}
	if nextPWM < 0 || nextPWM > 100 {
		return state, errors.New("pwm must be between 0 and 100")
	}

	nextPower := "off"
	if nextPWM > 0 {
		nextPower = "on"
	}

	if err := writeOutputPower(channel, nextPower, outputPathTemplate); err != nil {
		return state, err
	}

	nextState := cloneState(state)
	nextState.Outputs[channel-1].PWM = nextPWM
	nextState.Outputs[channel-1].Power = nextPower
	return nextState, nil
}

func applyLabel(state appState, target string, channel int, nextLabel string) (appState, error) {
	sanitizedLabel := strings.TrimSpace(nextLabel)
	if sanitizedLabel == "" {
		return state, errors.New("label is required")
	}

	nextState := cloneState(state)
	switch target {
	case "input":
		if channel < 1 || channel > inputCount {
			return state, errors.New("invalid input channel")
		}
		nextState.Inputs[channel-1].Label = sanitizedLabel
	case "output":
		if channel < 1 || channel > outputCount {
			return state, errors.New("invalid output channel")
		}
		nextState.Outputs[channel-1].Label = sanitizedLabel
	default:
		return state, errors.New("target must be input or output")
	}

	return nextState, nil
}

func cloneState(source appState) appState {
	copiedInputs := make([]inputState, len(source.Inputs))
	copy(copiedInputs, source.Inputs)

	copiedOutputs := make([]outputState, len(source.Outputs))
	copy(copiedOutputs, source.Outputs)

	return appState{Inputs: copiedInputs, Outputs: copiedOutputs}
}

func buildInitialInputMetrics() []inputMetric {
	initialMetrics := make([]inputMetric, inputCount)
	for index := range initialMetrics {
		initialMetrics[index] = inputMetric{lastSignal: "off", lastHz: 0}
	}
	return initialMetrics
}

func refreshInputSignals(state *appState, inputMetrics []inputMetric, inputPathTemplate string, config runtimeConfig, currentTime time.Time) {
	for index := range state.Inputs {
		rawInputSignal, err := readInputSignal(index+1, inputPathTemplate)
		if err != nil {
			continue
		}

		nextVoltage, nextSignal := parseInputVoltageAndSignal(rawInputSignal, config)
		state.Inputs[index].Signal = nextSignal
		state.Inputs[index].Voltage = formatVoltage(nextVoltage)
		nextFrequency := updateFrequencyMetric(&inputMetrics[index], nextSignal, currentTime)
		state.Inputs[index].Hz = formatFrequency(nextFrequency)
	}
}

func parseInputVoltageAndSignal(rawInputSignal string, config runtimeConfig) (float64, string) {
	trimmedSignal := strings.TrimSpace(rawInputSignal)

	if parsedVoltage, err := strconv.ParseFloat(trimmedSignal, 64); err == nil {
		nextSignal := "off"
		if parsedVoltage >= config.inputThresholdVoltage {
			nextSignal = "on"
		}
		return parsedVoltage, nextSignal
	}

	switch trimmedSignal {
	case "1":
		return config.inputOnVoltage, "on"
	case "0":
		return config.inputOffVoltage, "off"
	}

	return config.inputOffVoltage, "off"
}

func updateFrequencyMetric(metric *inputMetric, nextSignal string, currentTime time.Time) float64 {
	if metric.lastSignal == "" {
		metric.lastSignal = nextSignal
		return metric.lastHz
	}

	if metric.lastSignal != nextSignal {
		if !metric.lastEdgeAt.IsZero() {
			secondsBetweenEdges := currentTime.Sub(metric.lastEdgeAt).Seconds()
			if secondsBetweenEdges > 0 {
				metric.lastHz = 1.0 / (2.0 * secondsBetweenEdges)
			}
		}
		metric.lastEdgeAt = currentTime
		metric.lastSignal = nextSignal
	}

	return metric.lastHz
}

func formatVoltage(voltage float64) string {
	return fmt.Sprintf("%.2fV", voltage)
}

func formatFrequency(frequencyHz float64) string {
	return fmt.Sprintf("%.2f Hz", frequencyHz)
}

func writeOutputPower(channel int, nextPower string, outputPathTemplate string) error {
	nextValue := "0"
	if nextPower == "on" {
		nextValue = "1"
	}

	outputPath := fmt.Sprintf(outputPathTemplate, channel)
	if err := os.WriteFile(outputPath, []byte(nextValue), 0o644); err != nil {
		return fmt.Errorf("write DIO output %q: %w", outputPath, err)
	}

	return nil
}

func readInputSignal(channel int, inputPathTemplate string) (string, error) {
	inputPath := fmt.Sprintf(inputPathTemplate, channel)
	rawSignal, err := os.ReadFile(inputPath)
	if err != nil {
		return "off", err
	}

	return strings.TrimSpace(string(rawSignal)), nil
}

func loadLabels(labelsFile string) map[string]string {
	fileData, err := os.ReadFile(labelsFile)
	if err != nil {
		return map[string]string{}
	}

	var labels map[string]string
	if err := json.Unmarshal(fileData, &labels); err != nil {
		return map[string]string{}
	}

	return labels
}

func saveLabels(labelsFile string, state appState) error {
	labels := map[string]string{}
	for _, singleInput := range state.Inputs {
		labels["input-"+strconv.Itoa(singleInput.Channel)] = singleInput.Label
	}
	for _, singleOutput := range state.Outputs {
		labels["output-"+strconv.Itoa(singleOutput.Channel)] = singleOutput.Label
	}

	fileData, err := json.MarshalIndent(labels, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(labelsFile, fileData, 0o644)
}

// =============================
// HTTP handlers.
// =============================

func handleGetState(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "get", reply: reply}

		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(writer, result.state)
	}
}

func handleSetOutputPower(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setOutputPowerRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_output_power", channel: apiRequest.Channel, power: apiRequest.Power, reply: reply}

		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(writer, result.state)
	}
}

func handleSetOutputPWM(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setOutputPWMRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_output_pwm", channel: apiRequest.Channel, pwm: apiRequest.PWM, reply: reply}

		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(writer, result.state)
	}
}

func handleSetLabel(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var apiRequest setLabelRequest
		if err := json.NewDecoder(request.Body).Decode(&apiRequest); err != nil {
			http.Error(writer, "invalid json", http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_label", target: apiRequest.Kind, channel: apiRequest.Channel, label: apiRequest.Label, reply: reply}

		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(writer, result.state)
	}
}

func writeJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(payload)
}

// =============================
// Static file serving.
// =============================

func handleRequest(writer http.ResponseWriter, request *http.Request) {
	requestedFile := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if requestedFile == "" || requestedFile == "." {
		requestedFile = "index.html"
	}

	fullPathToFile := filepath.Join(*directoryFlag, filepath.FromSlash(requestedFile))
	if fileExists(fullPathToFile) {
		http.ServeFile(writer, request, fullPathToFile)
		return
	}

	if serveEmbeddedFile(writer, requestedFile) {
		return
	}

	http.NotFound(writer, request)
}

func serveEmbeddedFile(writer http.ResponseWriter, filename string) bool {
	staticSubtree, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("static: fs.Sub failed: %v", err)
		return false
	}

	cleanName := path.Clean(filename)
	if cleanName == "." || cleanName == "/" {
		cleanName = "index.html"
	}
	cleanName = strings.TrimPrefix(cleanName, "/")

	fileData, err := fs.ReadFile(staticSubtree, cleanName)
	if err != nil {
		return false
	}

	contentType := getContentType(cleanName)
	if contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}

	_, _ = writer.Write(fileData)
	return true
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return err == nil && !info.IsDir()
}

func getContentType(filename string) string {
	extension := strings.ToLower(filepath.Ext(filename))

	switch extension {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}

	detectedType := mime.TypeByExtension(extension)
	if detectedType != "" {
		return detectedType
	}

	return "application/octet-stream"
}
