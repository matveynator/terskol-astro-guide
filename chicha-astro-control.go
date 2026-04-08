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
	"os/exec"
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
	inputPathTemplateFlag  = flag.String("DI", "", "DI path template with %d placeholder")
	outputPathTemplateFlag = flag.String("DO", "", "DO path template with %d placeholder")
	settingsFileFlag       = flag.String("config", "", "path to settings json file")
)

const (
	inputCount           = 8
	outputCount          = 8
	serverStartupTimeout = 45 * time.Second
	defaultStartPort     = 7654
	defaultInputOnV      = 24.0
	defaultInputOffV     = 0.0
	defaultInputThreshV  = 2.0
	defaultPWMFrequency  = 100.0
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

type savedOutputState struct {
	Channel int    `json:"channel"`
	Power   string `json:"power"`
	PWM     int    `json:"pwm"`
}

type persistedSettings struct {
	Labels  map[string]string  `json:"labels"`
	Outputs []savedOutputState `json:"outputs"`
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

type pwmController struct {
	channelCommands []chan pwmChannelCommand
}

type pwmChannelCommand struct {
	power string
	pwm   int
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
		inputTemplate:  resolveIOPathTemplate(*inputPathTemplateFlag, defaultDIPathTemplate()),
		outputTemplate: resolveIOPathTemplate(*outputPathTemplateFlag, defaultDOPathTemplate()),
	}

	stateCommands := make(chan stateCommand)
	outputPWMController := startPWMController(resolvedIOPaths.outputTemplate, defaultPWMFrequency)
	settingsFile, err := resolveSettingsFilePath(*settingsFileFlag)
	if err != nil {
		log.Fatalf("startup: settings path resolve failed: %v", err)
	}
	go runStateOwner(
		stateCommands,
		resolvedIOPaths,
		runtimeConfig{
			inputOnVoltage:        defaultInputOnV,
			inputOffVoltage:       defaultInputOffV,
			inputThresholdVoltage: defaultInputThreshV,
		},
		settingsFile,
		outputPWMController,
	)

	http.HandleFunc("/api/state", handleGetState(stateCommands))
	http.HandleFunc("/api/output/power", handleSetOutputPower(stateCommands))
	http.HandleFunc("/api/output/pwm", handleSetOutputPWM(stateCommands))
	http.HandleFunc("/api/label", handleSetLabel(stateCommands))
	http.HandleFunc("/api/open/repository", handleOpenRepository)
	http.HandleFunc("/", handleRequest)

	httpListener, address := listenOnFirstAvailablePort(defaultStartPort)
	log.Printf("startup: starting HTTP server on http://%s", address)

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

	window.SetTitle("astro-control")
	window.SetSize(1120, 760, webview.HintNone)
	window.Navigate("http://" + address)

	log.Printf("webview: window started")
	window.Run()
	log.Printf("shutdown: webview stopped")
}

func resolveIOPathTemplate(explicitTemplate string, fallbackTemplate string) string {
	trimmedExplicitTemplate := strings.TrimSpace(explicitTemplate)
	if trimmedExplicitTemplate == "" {
		return fallbackTemplate
	}

	log.Printf("dio: using template=%s", trimmedExplicitTemplate)
	return trimmedExplicitTemplate
}

func defaultDIPathTemplate() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Vecow\ECX1K\di%d.value`
	case "darwin":
		return "/tmp/astro-control/di%d.value"
	default:
		return "/sys/class/gpio/gpio%d/value"
	}
}

func defaultDOPathTemplate() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Vecow\ECX1K\do%d.value`
	case "darwin":
		return "/tmp/astro-control/do%d.value"
	default:
		return "/sys/class/gpio/gpio%d/value"
	}
}

func listenOnFirstAvailablePort(startPort int) (net.Listener, string) {
	for portNumber := startPort; portNumber <= 65535; portNumber++ {
		address := fmt.Sprintf("127.0.0.1:%d", portNumber)
		httpListener, err := net.Listen("tcp", address)
		if err == nil {
			return httpListener, address
		}
	}

	log.Fatalf("startup: unable to find free port from %d", startPort)
	return nil, ""
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

func handleOpenRepository(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		responseWriter.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	err := openURLInExternalBrowser("https://github.com/matveynator/chicha-astro-control")
	if err != nil {
		writeJSONError(responseWriter, http.StatusInternalServerError, "failed to open external browser")
		log.Printf("repository: open external browser failed: %v", err)
		return
	}

	responseWriter.WriteHeader(http.StatusNoContent)
}

func openURLInExternalBrowser(repositoryURL string) error {
	var commandName string
	var commandArguments []string

	switch runtime.GOOS {
	case "windows":
		commandName = "cmd"
		commandArguments = []string{"/c", "start", "", repositoryURL}
	case "darwin":
		commandName = "open"
		commandArguments = []string{"-n", repositoryURL}
	default:
		commandName = "xdg-open"
		commandArguments = []string{repositoryURL}
	}

	openCommand := exec.Command(commandName, commandArguments...)
	if err := openCommand.Start(); err != nil {
		return err
	}

	go func() {
		if err := openCommand.Wait(); err != nil {
			log.Printf("repository: external browser command finished with error: %v", err)
		}
	}()

	return nil
}

// =============================
// State owner goroutine.
// =============================

func runStateOwner(stateCommands <-chan stateCommand, resolvedIOPaths ioPaths, config runtimeConfig, settingsFile string, outputPWMController *pwmController) {
	loadedSettings := loadSettings(settingsFile)
	state := buildInitialState(loadedSettings.Labels, indexOutputsByChannel(loadedSettings.Outputs))
	for _, configuredOutput := range state.Outputs {
		outputPWMController.Apply(configuredOutput)
	}
	inputMetrics := buildInitialInputMetrics()
	refreshInputSignals(&state, inputMetrics, resolvedIOPaths.inputTemplate, config, time.Now())

	for command := range stateCommands {
		switch command.kind {
		case "get":
			refreshInputSignals(&state, inputMetrics, resolvedIOPaths.inputTemplate, config, time.Now())
			command.reply <- stateReply{state: cloneState(state)}

		case "set_output_power":
			nextState, err := applyOutputPower(state, command.channel, command.power)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveSettings(settingsFile, nextState); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			outputPWMController.Apply(nextState.Outputs[command.channel-1])
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		case "set_output_pwm":
			nextState, err := applyOutputPWM(state, command.channel, command.pwm)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveSettings(settingsFile, nextState); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			outputPWMController.Apply(nextState.Outputs[command.channel-1])
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		case "set_label":
			nextState, err := applyLabel(state, command.target, command.channel, command.label)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveSettings(settingsFile, nextState); err != nil {
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

func buildInitialState(savedLabels map[string]string, savedOutputs map[int]savedOutputState) appState {
	inputs := make([]inputState, 0, inputCount)
	for channelIndex := 1; channelIndex <= inputCount; channelIndex++ {
		labelKey := "input-" + strconv.Itoa(channelIndex)
		label := normalizeInputLabel(channelIndex, savedLabels[labelKey])

		inputs = append(inputs, inputState{Channel: channelIndex, Signal: "off", Voltage: "0.0V", Hz: "0.00 Hz", Label: label})
	}

	outputs := make([]outputState, 0, outputCount)
	for channelIndex := 1; channelIndex <= outputCount; channelIndex++ {
		labelKey := "output-" + strconv.Itoa(channelIndex)
		label := normalizeOutputLabel(channelIndex, savedLabels[labelKey])

		initialPower := "off"
		initialPWM := 0
		if savedOutput, exists := savedOutputs[channelIndex]; exists {
			if savedOutput.Power == "on" || savedOutput.Power == "off" {
				initialPower = savedOutput.Power
			}
			if savedOutput.PWM >= 0 && savedOutput.PWM <= 100 {
				initialPWM = savedOutput.PWM
			}
		}

		outputs = append(outputs, outputState{Channel: channelIndex, Power: initialPower, PWM: initialPWM, Label: label})
	}

	return appState{Inputs: inputs, Outputs: outputs}
}

func applyOutputPower(state appState, channel int, nextPower string) (appState, error) {
	if channel < 1 || channel > outputCount {
		return state, errors.New("invalid output channel")
	}
	if nextPower != "on" && nextPower != "off" {
		return state, errors.New("power must be on or off")
	}

	nextState := cloneState(state)
	nextState.Outputs[channel-1].Power = nextPower
	if nextPower == "on" && nextState.Outputs[channel-1].PWM == 0 {
		nextState.Outputs[channel-1].PWM = 100
	}

	return nextState, nil
}

func applyOutputPWM(state appState, channel int, nextPWM int) (appState, error) {
	if channel < 1 || channel > outputCount {
		return state, errors.New("invalid output channel")
	}
	if nextPWM < 0 || nextPWM > 100 {
		return state, errors.New("pwm must be between 0 and 100")
	}

	nextState := cloneState(state)
	nextState.Outputs[channel-1].PWM = nextPWM
	return nextState, nil
}

func applyLabel(state appState, target string, channel int, nextLabel string) (appState, error) {
	sanitizedLabel := strings.TrimSpace(nextLabel)

	nextState := cloneState(state)
	switch target {
	case "input":
		if channel < 1 || channel > inputCount {
			return state, errors.New("invalid input channel")
		}
		if sanitizedLabel == "" {
			sanitizedLabel = defaultInputLabel(channel)
		}
		nextState.Inputs[channel-1].Label = sanitizedLabel
	case "output":
		if channel < 1 || channel > outputCount {
			return state, errors.New("invalid output channel")
		}
		if sanitizedLabel == "" {
			sanitizedLabel = defaultOutputLabel(channel)
		}
		nextState.Outputs[channel-1].Label = sanitizedLabel
	default:
		return state, errors.New("target must be input or output")
	}

	return nextState, nil
}

func defaultInputLabel(channel int) string {
	return fmt.Sprintf("DI%d", channel)
}

func defaultOutputLabel(channel int) string {
	return fmt.Sprintf("DO%d", channel+10)
}

func normalizeInputLabel(channel int, rawLabel string) string {
	sanitizedLabel := strings.TrimSpace(rawLabel)
	defaultLabel := defaultInputLabel(channel)
	if sanitizedLabel == "" {
		return defaultLabel
	}

	prefix, parsedNumber, isPortLabel := parsePortLabel(sanitizedLabel)
	if !isPortLabel {
		return sanitizedLabel
	}

	if prefix != "DI" || parsedNumber != channel {
		return defaultLabel
	}

	return defaultLabel
}

func normalizeOutputLabel(channel int, rawLabel string) string {
	sanitizedLabel := strings.TrimSpace(rawLabel)
	defaultLabel := defaultOutputLabel(channel)
	if sanitizedLabel == "" {
		return defaultLabel
	}

	prefix, parsedNumber, isPortLabel := parsePortLabel(sanitizedLabel)
	if !isPortLabel {
		return sanitizedLabel
	}

	if prefix != "DO" || parsedNumber != channel+10 {
		return defaultLabel
	}

	return defaultLabel
}

func parsePortLabel(rawLabel string) (string, int, bool) {
	compactLabel := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(rawLabel), " ", ""))
	if len(compactLabel) < 3 {
		return "", 0, false
	}

	prefix := compactLabel[:2]
	if prefix != "DI" && prefix != "DO" {
		return "", 0, false
	}

	parsedNumber, err := strconv.Atoi(compactLabel[2:])
	if err != nil {
		return "", 0, false
	}

	return prefix, parsedNumber, true
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

	// Treat common GPIO digital tokens first so sysfs "0"/"1" keep explicit digital voltage mapping.
	switch trimmedSignal {
	case "1":
		return config.inputOnVoltage, "on"
	case "0":
		return config.inputOffVoltage, "off"
	}

	if parsedVoltage, err := strconv.ParseFloat(trimmedSignal, 64); err == nil {
		nextSignal := "off"
		if parsedVoltage >= config.inputThresholdVoltage {
			nextSignal = "on"
		}
		return parsedVoltage, nextSignal
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

func startPWMController(outputPathTemplate string, pwmFrequencyHz float64) *pwmController {
	if pwmFrequencyHz <= 0 {
		pwmFrequencyHz = 100
	}

	controller := &pwmController{
		channelCommands: make([]chan pwmChannelCommand, outputCount),
	}

	for channel := 1; channel <= outputCount; channel++ {
		channelCommandQueue := make(chan pwmChannelCommand, 1)
		controller.channelCommands[channel-1] = channelCommandQueue
		go runPWMChannelLoop(channel, fmt.Sprintf(outputPathTemplate, channel), pwmFrequencyHz, channelCommandQueue)
	}

	return controller
}

func (controller *pwmController) Apply(output outputState) {
	if output.Channel < 1 || output.Channel > len(controller.channelCommands) {
		return
	}

	nextCommand := pwmChannelCommand{power: output.Power, pwm: output.PWM}
	channelCommandQueue := controller.channelCommands[output.Channel-1]
	for attemptIndex := 0; attemptIndex < 3; attemptIndex++ {
		select {
		case channelCommandQueue <- nextCommand:
			return
		default:
		}

		// Drop the stale queued command only when one is currently buffered.
		select {
		case <-channelCommandQueue:
		default:
		}
	}
}

func runPWMChannelLoop(channel int, outputPath string, pwmFrequencyHz float64, channelCommands <-chan pwmChannelCommand) {
	currentCommand := pwmChannelCommand{power: "off", pwm: 0}
	pwmPeriod := time.Duration(float64(time.Second) / pwmFrequencyHz)
	log.Printf("pwm: channel=%d path=%s frequency=%.2fHz", channel, outputPath, pwmFrequencyHz)

	for {
		if currentCommand.power != "on" || currentCommand.pwm <= 0 {
			if err := writeOutputRaw(outputPath, "0"); err != nil {
				log.Printf("pwm: channel=%d write low failed: %v", channel, err)
			}
			currentCommand = <-channelCommands
			continue
		}

		if currentCommand.pwm >= 100 {
			if err := writeOutputRaw(outputPath, "1"); err != nil {
				log.Printf("pwm: channel=%d write high failed: %v", channel, err)
			}
			currentCommand = <-channelCommands
			continue
		}

		onDuration := time.Duration(float64(pwmPeriod) * (float64(currentCommand.pwm) / 100.0))
		offDuration := pwmPeriod - onDuration

		if err := writeOutputRaw(outputPath, "1"); err != nil {
			log.Printf("pwm: channel=%d write high failed: %v", channel, err)
		}
		if hasNewCommand, nextCommand := waitPWMStage(channelCommands, onDuration); hasNewCommand {
			currentCommand = nextCommand
			continue
		}

		if err := writeOutputRaw(outputPath, "0"); err != nil {
			log.Printf("pwm: channel=%d write low failed: %v", channel, err)
		}
		if hasNewCommand, nextCommand := waitPWMStage(channelCommands, offDuration); hasNewCommand {
			currentCommand = nextCommand
			continue
		}
	}
}

func waitPWMStage(channelCommands <-chan pwmChannelCommand, stageDuration time.Duration) (bool, pwmChannelCommand) {
	if stageDuration <= 0 {
		select {
		case nextCommand := <-channelCommands:
			return true, nextCommand
		default:
			return false, pwmChannelCommand{}
		}
	}

	stageTimer := time.NewTimer(stageDuration)
	defer stageTimer.Stop()

	select {
	case nextCommand := <-channelCommands:
		return true, nextCommand
	case <-stageTimer.C:
		return false, pwmChannelCommand{}
	}
}

func writeOutputPower(channel int, nextPower string, outputPathTemplate string) error {
	nextValue := "0"
	if nextPower == "on" {
		nextValue = "1"
	}

	outputPath := fmt.Sprintf(outputPathTemplate, channel)
	return writeOutputRaw(outputPath, nextValue)
}

func writeOutputRaw(outputPath string, nextValue string) error {
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

func resolveSettingsFilePath(explicitSettingsFile string) (string, error) {
	trimmedSettingsPath := strings.TrimSpace(explicitSettingsFile)
	if trimmedSettingsPath != "" {
		return trimmedSettingsPath, ensureSettingsFile(trimmedSettingsPath)
	}

	applicationName := filepath.Base(os.Args[0])
	applicationExtension := filepath.Ext(applicationName)
	if applicationExtension != "" {
		applicationName = strings.TrimSuffix(applicationName, applicationExtension)
	}
	if applicationName == "" {
		applicationName = "app"
	}

	settingsDirectory := ""
	if runtime.GOOS == "windows" {
		settingsDirectory = `C:/appname.conf`
	} else {
		homeDirectory, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		settingsDirectory = filepath.Join(homeDirectory, ".appname.conf")
	}

	settingsFile := filepath.Join(settingsDirectory, applicationName+".json")
	return settingsFile, ensureSettingsFile(settingsFile)
}

func ensureSettingsFile(settingsFile string) error {
	settingsDirectory := filepath.Dir(settingsFile)
	if err := os.MkdirAll(settingsDirectory, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(settingsFile); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.WriteFile(settingsFile, []byte("{}\n"), 0o644)
}

func loadSettings(settingsFile string) persistedSettings {
	fileData, err := os.ReadFile(settingsFile)
	if err != nil {
		return persistedSettings{Labels: map[string]string{}, Outputs: []savedOutputState{}}
	}

	var settings persistedSettings
	if err := json.Unmarshal(fileData, &settings); err != nil {
		return persistedSettings{Labels: map[string]string{}, Outputs: []savedOutputState{}}
	}

	if settings.Labels == nil {
		settings.Labels = map[string]string{}
	}
	if settings.Outputs == nil {
		settings.Outputs = []savedOutputState{}
	}

	return settings
}

func indexOutputsByChannel(savedOutputs []savedOutputState) map[int]savedOutputState {
	outputsByChannel := make(map[int]savedOutputState, len(savedOutputs))
	for _, singleOutput := range savedOutputs {
		outputsByChannel[singleOutput.Channel] = singleOutput
	}
	return outputsByChannel
}

func saveSettings(settingsFile string, state appState) error {
	labels := map[string]string{}
	for _, singleInput := range state.Inputs {
		labels["input-"+strconv.Itoa(singleInput.Channel)] = singleInput.Label
	}
	for _, singleOutput := range state.Outputs {
		labels["output-"+strconv.Itoa(singleOutput.Channel)] = singleOutput.Label
	}

	savedOutputs := make([]savedOutputState, 0, len(state.Outputs))
	for _, singleOutput := range state.Outputs {
		savedOutputs = append(savedOutputs, savedOutputState{
			Channel: singleOutput.Channel,
			Power:   singleOutput.Power,
			PWM:     singleOutput.PWM,
		})
	}

	settings := persistedSettings{Labels: labels, Outputs: savedOutputs}
	fileData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(settingsFile, fileData, 0o644)
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

func writeJSONError(writer http.ResponseWriter, statusCode int, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"error": message,
	})
}

// =============================
// Static file serving.
// =============================

func handleRequest(writer http.ResponseWriter, request *http.Request) {
	requestedFile := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if requestedFile == "" || requestedFile == "." {
		requestedFile = "index.html"
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
