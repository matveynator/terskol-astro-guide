package main

import (
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"chicha-astro-control/pkg/gpio"
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
	defaultPWMFrequency  = 100.0
)

// =============================
// Domain model.
// =============================

type inputState struct {
	Channel int    `json:"channel"`
	Signal  string `json:"signal"`
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
	Runtime runtimeState  `json:"runtime"`
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

type runtimeState struct {
	TestMode        bool   `json:"test_mode"`
	Message         string `json:"message"`
	MessageKey      string `json:"message_key"`
	DLLOverridePath string `json:"dll_override_path"`
}

type savedOutputState struct {
	Channel int    `json:"channel"`
	Power   string `json:"power"`
	PWM     int    `json:"pwm"`
}

type ioRuntimeMode struct {
	inputSimulation  bool
	outputSimulation bool
	state            runtimeState
}

type persistedSettings struct {
	Labels          map[string]string  `json:"labels"`
	Outputs         []savedOutputState `json:"outputs"`
	DLLOverridePath string             `json:"dll_override_path"`
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

type hotSwapGPIOAdapter struct {
	currentAdapter atomic.Value
	inputSimulated atomic.Bool
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
	configureApplicationLogging()
	flag.Parse()

	resolvedIOPaths := ioPaths{
		inputTemplate:  resolveIOPathTemplate(*inputPathTemplateFlag, gpio.DefaultInputTemplate()),
		outputTemplate: resolveIOPathTemplate(*outputPathTemplateFlag, gpio.DefaultOutputTemplate()),
	}
	settingsFile, err := resolveSettingsFilePath(*settingsFileFlag)
	if err != nil {
		log.Fatalf("startup: settings path resolve failed: %v", err)
	}
	loadedSettings := loadSettings(settingsFile)

	cleanupWindowsDriverDirectory, err := gpio.PrepareWindowsDriverDirectory(staticFiles)
	if err != nil {
		log.Fatalf("startup: windows driver prepare failed: %v", err)
	}
	defer cleanupWindowsDriverDirectory()

	gpioAdapter, runtimeMode, err := gpio.Open(gpio.Config{
		InputTemplate:  resolvedIOPaths.inputTemplate,
		OutputTemplate: resolvedIOPaths.outputTemplate,
		WindowsDLLPath: loadedSettings.DLLOverridePath,
	})
	if err != nil {
		log.Printf("startup: GPIO init failed, continue in simulation mode: %v", err)
		gpioAdapter = gpio.SimulationAdapter{}
		runtimeMode = gpio.RuntimeMode{
			InputSimulation:  true,
			OutputSimulation: true,
			DriverProbeLog:   gpio.ProbeLogFromError(err),
		}
	}
	hotSwapAdapter := newHotSwapGPIOAdapter(gpioAdapter, runtimeMode)
	defer func() {
		if closeErr := hotSwapAdapter.Close(); closeErr != nil {
			log.Printf("shutdown: GPIO close failed: %v", closeErr)
		}
	}()
	logRuntimeMode(runtimeMode, resolvedIOPaths)

	stateCommands := make(chan stateCommand)
	runtimeStateData := buildRuntimeStateForUI(runtimeMode, loadedSettings.DLLOverridePath)

	outputPWMController := startPWMController(hotSwapAdapter, defaultPWMFrequency)
	go runStateOwner(
		stateCommands,
		hotSwapAdapter,
		settingsFile,
		loadedSettings,
		outputPWMController,
		runtimeMode,
		resolvedIOPaths,
		runtimeStateData,
	)

	http.HandleFunc("/api/state", handleGetState(stateCommands))
	http.HandleFunc("/api/state/ws", handleStateWebSocket(stateCommands))
	http.HandleFunc("/api/control/ws", handleControlWebSocket(stateCommands))
	http.HandleFunc("/api/output/power", handleSetOutputPower(stateCommands))
	http.HandleFunc("/api/output/pwm", handleSetOutputPWM(stateCommands))
	http.HandleFunc("/api/label", handleSetLabel(stateCommands))
	http.HandleFunc("/api/open/repository", handleOpenRepository)
	http.HandleFunc("/api/runtime/dll-override", handleSetDLLOverride(stateCommands))
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

	window.SetTitle("chicha-astro-control")
	window.SetSize(1120, 760, webview.HintNone)
	window.Navigate("http://" + address)

	log.Printf("webview: window started")
	window.Run()
	log.Printf("shutdown: webview stopped")
}

func configureApplicationLogging() {
	logFilePath, pathErr := resolveLogFilePath()
	if pathErr != nil {
		log.Printf("startup: resolve log file path failed: %v", pathErr)
		return
	}

	logDirectory := filepath.Dir(logFilePath)
	if mkdirErr := os.MkdirAll(logDirectory, 0o755); mkdirErr != nil {
		log.Printf("startup: create log directory failed: %v", mkdirErr)
		return
	}

	logFileHandle, openErr := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if openErr != nil {
		log.Printf("startup: open log file failed: %v", openErr)
		return
	}

	log.SetOutput(buildLogOutputWriter(logFileHandle))
	log.Printf("startup: logging to %s", logFilePath)
}

func buildLogOutputWriter(logFileHandle *os.File) io.Writer {
	if logFileHandle == nil {
		return os.Stderr
	}

	if stderrIsUnavailable() {
		return logFileHandle
	}

	return io.MultiWriter(logFileHandle, os.Stderr)
}

func stderrIsUnavailable() bool {
	if os.Stderr == nil {
		return true
	}

	if _, statErr := os.Stderr.Stat(); statErr != nil {
		return true
	}
	return false
}

func resolveLogFilePath() (string, error) {
	if runtime.GOOS == "windows" {
		localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			return "", fmt.Errorf("LOCALAPPDATA is empty")
		}
		return filepath.Join(localAppData, "chicha-astro-control", "logs", "application.log"), nil
	}

	userCacheDirectory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(userCacheDirectory, "chicha-astro-control", "application.log"), nil
}

func resolveIOPathTemplate(explicitTemplate string, fallbackTemplate string) string {
	trimmedExplicitTemplate := strings.TrimSpace(explicitTemplate)
	if trimmedExplicitTemplate == "" {
		return fallbackTemplate
	}

	log.Printf("dio: using template=%s", trimmedExplicitTemplate)
	return trimmedExplicitTemplate
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

func logRuntimeMode(mode gpio.RuntimeMode, resolvedIOPaths ioPaths) {
	if mode.ActiveDriver != "" {
		log.Printf("gpio: windows driver loaded successfully: %s", mode.ActiveDriver)
	}
	if mode.InputSimulation {
		log.Printf("gpio: inputs are unavailable, switch to simulation mode. template=%s", resolvedIOPaths.inputTemplate)
	}
	if mode.OutputSimulation {
		log.Printf("gpio: outputs are unavailable, switch to simulation mode. template=%s", resolvedIOPaths.outputTemplate)
	}
}

func buildRuntimeStateForUI(mode gpio.RuntimeMode, dllOverridePath string) runtimeState {
	assembledState := runtimeState{}
	if mode.InputSimulation || mode.OutputSimulation {
		assembledState.TestMode = true
		assembledState.MessageKey = "runtime_demo_mode"
	}
	assembledState.DLLOverridePath = strings.TrimSpace(dllOverridePath)

	probeMessageParts := make([]string, 0, 2)
	if mode.DriverProbeLog != "" {
		probeMessageParts = append(probeMessageParts, mode.DriverProbeLog)
	}
	if mode.ActiveDriver != "" {
		probeMessageParts = append(probeMessageParts, fmt.Sprintf("Selected DLL: %s", mode.ActiveDriver))
	}
	if len(probeMessageParts) > 0 {
		assembledState.Message = strings.Join(probeMessageParts, "\n")
	}
	return assembledState
}

func newHotSwapGPIOAdapter(initialAdapter gpio.Adapter, initialMode gpio.RuntimeMode) *hotSwapGPIOAdapter {
	adapter := &hotSwapGPIOAdapter{}
	adapter.currentAdapter.Store(initialAdapter)
	adapter.inputSimulated.Store(initialMode.InputSimulation)
	return adapter
}

func (adapter *hotSwapGPIOAdapter) ReadInput(channel int) (bool, error) {
	if adapter.inputSimulated.Load() {
		return false, nil
	}
	activeAdapter := adapter.currentAdapter.Load()
	if activeAdapter == nil {
		return false, errors.New("gpio adapter is not initialized")
	}
	return activeAdapter.(gpio.Adapter).ReadInput(channel)
}

func (adapter *hotSwapGPIOAdapter) WriteOutput(channel int, high bool) error {
	activeAdapter := adapter.currentAdapter.Load()
	if activeAdapter == nil {
		return errors.New("gpio adapter is not initialized")
	}
	return activeAdapter.(gpio.Adapter).WriteOutput(channel, high)
}

func (adapter *hotSwapGPIOAdapter) Close() error {
	activeAdapter := adapter.currentAdapter.Load()
	if activeAdapter == nil {
		return nil
	}
	return activeAdapter.(gpio.Adapter).Close()
}

func (adapter *hotSwapGPIOAdapter) Swap(nextAdapter gpio.Adapter, nextMode gpio.RuntimeMode) error {
	previousAdapter := adapter.currentAdapter.Load()
	adapter.currentAdapter.Store(nextAdapter)
	adapter.inputSimulated.Store(nextMode.InputSimulation)
	if previousAdapter == nil {
		return nil
	}
	return previousAdapter.(gpio.Adapter).Close()
}

func detectIORuntimeMode(resolvedIOPaths ioPaths) ioRuntimeMode {
	mode := ioRuntimeMode{}
	if firstMissingChannelPath(resolvedIOPaths.inputTemplate, inputCount) != "" {
		mode.inputSimulation = true
	}
	if firstMissingChannelPath(resolvedIOPaths.outputTemplate, outputCount) != "" {
		mode.outputSimulation = true
	}
	if mode.inputSimulation || mode.outputSimulation {
		mode.state = runtimeState{TestMode: true, MessageKey: "runtime_demo_mode"}
	}
	return mode
}

func firstMissingChannelPath(pathTemplate string, channelCount int) string {
	return firstMissingChannelPathWithStat(pathTemplate, channelCount, os.Stat)
}

func firstMissingChannelPathWithStat(pathTemplate string, channelCount int, statPath func(string) (os.FileInfo, error)) string {
	for channel := 1; channel <= channelCount; channel++ {
		channelPath := fmt.Sprintf(pathTemplate, channel)
		if _, err := statPath(channelPath); err != nil {
			if !os.IsNotExist(err) {
				log.Printf("gpio: stat path failed but file is not missing, continue hardware mode. path=%s err=%v", channelPath, err)
				continue
			}
			return channelPath
		}
	}
	return ""
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

func runStateOwner(stateCommands <-chan stateCommand, gpioAdapter *hotSwapGPIOAdapter, settingsFile string, loadedSettings persistedSettings, outputPWMController *pwmController, runtimeMode gpio.RuntimeMode, resolvedIOPaths ioPaths, runtimeStateData runtimeState) {
	currentSettings := loadedSettings
	state := buildInitialState(loadedSettings.Labels, indexOutputsByChannel(loadedSettings.Outputs))
	state.Runtime = runtimeStateData
	currentRuntimeMode := runtimeMode
	for _, configuredOutput := range state.Outputs {
		outputPWMController.Apply(configuredOutput)
	}
	refreshInputSignals(&state, gpioAdapter)

	for command := range stateCommands {
		switch command.kind {
		case "get":
			refreshInputSignals(&state, gpioAdapter)
			command.reply <- stateReply{state: cloneState(state)}

		case "set_output_power":
			nextState, err := applyOutputPower(state, command.channel, command.power)
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			if err := saveSettings(settingsFile, nextState, currentSettings.DLLOverridePath); err != nil {
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
			if err := saveSettings(settingsFile, nextState, currentSettings.DLLOverridePath); err != nil {
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
			if err := saveSettings(settingsFile, nextState, currentSettings.DLLOverridePath); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			state = nextState
			command.reply <- stateReply{state: cloneState(state)}

		case "set_dll_override":
			dllOverridePath, err := applyDLLOverridePath(strings.TrimSpace(command.label))
			if err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}

			nextAdapter, nextRuntimeMode, openErr := gpio.Open(gpio.Config{
				InputTemplate:  resolvedIOPaths.inputTemplate,
				OutputTemplate: resolvedIOPaths.outputTemplate,
				WindowsDLLPath: dllOverridePath,
			})
			if openErr != nil {
				currentRuntimeMode = gpio.RuntimeMode{
					InputSimulation:  true,
					OutputSimulation: true,
					DriverProbeLog:   gpio.ProbeLogFromError(openErr),
				}
				state.Runtime = buildRuntimeStateForUI(currentRuntimeMode, currentSettings.DLLOverridePath)
				state.Runtime.MessageKey = "runtime_demo_mode"
				state.Runtime.DLLOverridePath = dllOverridePath
				command.reply <- stateReply{state: cloneState(state)}
				continue
			}

			if swapErr := gpioAdapter.Swap(nextAdapter, nextRuntimeMode); swapErr != nil {
				command.reply <- stateReply{state: cloneState(state), err: swapErr}
				continue
			}
			currentRuntimeMode = nextRuntimeMode
			currentSettings.DLLOverridePath = dllOverridePath
			state.Runtime = buildRuntimeStateForUI(currentRuntimeMode, currentSettings.DLLOverridePath)
			if err := saveSettings(settingsFile, state, currentSettings.DLLOverridePath); err != nil {
				command.reply <- stateReply{state: cloneState(state), err: err}
				continue
			}
			refreshInputSignals(&state, gpioAdapter)
			for _, configuredOutput := range state.Outputs {
				outputPWMController.Apply(configuredOutput)
			}
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

		inputs = append(inputs, inputState{Channel: channelIndex, Signal: "off", Label: label})
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

	return appState{Inputs: copiedInputs, Outputs: copiedOutputs, Runtime: source.Runtime}
}

func refreshInputSignals(state *appState, gpioAdapter gpio.Adapter) {
	for index := range state.Inputs {
		isOn, err := gpioAdapter.ReadInput(index + 1)
		if err != nil {
			continue
		}
		if isOn {
			state.Inputs[index].Signal = "on"
			continue
		}
		state.Inputs[index].Signal = "off"
	}
}

func parseInputSignal(rawInputSignal string) string {
	trimmedSignal := strings.TrimSpace(rawInputSignal)
	switch strings.ToLower(trimmedSignal) {
	case "1", "on", "high", "true":
		return "on"
	default:
		return "off"
	}
}

func startPWMController(gpioAdapter gpio.Adapter, pwmFrequencyHz float64) *pwmController {
	if pwmFrequencyHz <= 0 {
		pwmFrequencyHz = 100
	}

	controller := &pwmController{
		channelCommands: make([]chan pwmChannelCommand, outputCount),
	}

	for channel := 1; channel <= outputCount; channel++ {
		channelCommandQueue := make(chan pwmChannelCommand, 1)
		controller.channelCommands[channel-1] = channelCommandQueue
		go runPWMChannelLoop(channel, gpioAdapter, pwmFrequencyHz, channelCommandQueue)
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

func runPWMChannelLoop(channel int, gpioAdapter gpio.Adapter, pwmFrequencyHz float64, channelCommands <-chan pwmChannelCommand) {
	currentCommand := pwmChannelCommand{power: "off", pwm: 0}
	pwmPeriod := time.Duration(float64(time.Second) / pwmFrequencyHz)
	log.Printf("pwm: channel=%d frequency=%.2fHz", channel, pwmFrequencyHz)

	for {
		if currentCommand.power != "on" || currentCommand.pwm <= 0 {
			if err := gpioAdapter.WriteOutput(channel, false); err != nil {
				log.Printf("pwm: channel=%d write low failed: %v", channel, err)
			}
			currentCommand = <-channelCommands
			continue
		}

		if currentCommand.pwm >= 100 {
			if err := gpioAdapter.WriteOutput(channel, true); err != nil {
				log.Printf("pwm: channel=%d write high failed: %v", channel, err)
			}
			currentCommand = <-channelCommands
			continue
		}

		onDuration := time.Duration(float64(pwmPeriod) * (float64(currentCommand.pwm) / 100.0))
		offDuration := pwmPeriod - onDuration

		if err := gpioAdapter.WriteOutput(channel, true); err != nil {
			log.Printf("pwm: channel=%d write high failed: %v", channel, err)
		}
		if hasNewCommand, nextCommand := waitPWMStage(channelCommands, onDuration); hasNewCommand {
			currentCommand = nextCommand
			continue
		}

		if err := gpioAdapter.WriteOutput(channel, false); err != nil {
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
		settingsDirectory = `C:/`
	} else {
		homeDirectory, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		settingsDirectory = homeDirectory
	}

	normalizedApplicationName := strings.ToLower(applicationName)
	settingsFile := filepath.Join(settingsDirectory, "."+normalizedApplicationName+".conf")
	if runtime.GOOS == "windows" {
		settingsFile = filepath.Join(settingsDirectory, normalizedApplicationName+".conf")
	}
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

func applyDLLOverridePath(rawPath string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("DLL override is available only on Windows")
	}
	if rawPath == "" {
		return "", errors.New("dll path is required")
	}
	if !strings.EqualFold(filepath.Ext(rawPath), ".dll") {
		return "", errors.New("selected file must have .dll extension")
	}

	fileInfo, err := os.Stat(rawPath)
	if err != nil {
		return "", fmt.Errorf("failed to read DLL file: %w", err)
	}
	if fileInfo.IsDir() {
		return "", errors.New("selected path points to a directory")
	}

	absolutePath, err := filepath.Abs(rawPath)
	if err != nil {
		return "", fmt.Errorf("failed to normalize DLL path: %w", err)
	}
	return absolutePath, nil
}

func saveSettings(settingsFile string, state appState, dllOverridePath string) error {
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

	settings := persistedSettings{
		Labels:          labels,
		Outputs:         savedOutputs,
		DLLOverridePath: strings.TrimSpace(dllOverridePath),
	}
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
		result := requestStateSnapshot(stateCommands)
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusInternalServerError)
			return
		}

		writeJSON(writer, result.state)
	}
}

func handleStateWebSocket(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		websocketConnection, err := upgradeToWebSocket(writer, request)
		if err != nil {
			http.Error(writer, "websocket upgrade failed", http.StatusBadRequest)
			return
		}
		defer websocketConnection.Close()

		latestStateReply := requestStateSnapshot(stateCommands)
		if latestStateReply.err != nil {
			log.Printf("websocket: initial state failed: %v", latestStateReply.err)
			return
		}
		if err := websocketConnection.WriteState(latestStateReply.state); err != nil {
			return
		}

		streamTicker := time.NewTicker(350 * time.Millisecond)
		defer streamTicker.Stop()
		for {
			<-streamTicker.C
			currentStateReply := requestStateSnapshot(stateCommands)
			if currentStateReply.err != nil {
				return
			}
			if err := websocketConnection.WriteState(currentStateReply.state); err != nil {
				return
			}
		}
	}
}

type webSocketConnection struct {
	socket net.Conn
}

type controlWebSocketRequest struct {
	RequestID string `json:"request_id"`
	Kind      string `json:"kind"`
	Power     string `json:"power"`
	PWM       int    `json:"pwm"`
}

type controlWebSocketResponse struct {
	RequestID string   `json:"request_id"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	Channel   int      `json:"channel"`
	Kind      string   `json:"kind"`
	State     appState `json:"state"`
}

func upgradeToWebSocket(writer http.ResponseWriter, request *http.Request) (*webSocketConnection, error) {
	if !isWebSocketOriginAllowed(request) {
		return nil, errors.New("origin is not allowed")
	}
	if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("missing websocket upgrade header")
	}
	if !strings.Contains(strings.ToLower(request.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("missing connection upgrade token")
	}
	websocketKey := strings.TrimSpace(request.Header.Get("Sec-WebSocket-Key"))
	if websocketKey == "" {
		return nil, errors.New("missing websocket key")
	}

	websocketAcceptor := buildWebSocketAcceptValue(websocketKey)
	hijacker, canHijack := writer.(http.Hijacker)
	if !canHijack {
		return nil, errors.New("http hijacker unavailable")
	}
	socket, bufferedWriter, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	upgradeResponse := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + websocketAcceptor + "\r\n\r\n"
	if _, err := bufferedWriter.WriteString(upgradeResponse); err != nil {
		_ = socket.Close()
		return nil, err
	}
	if err := bufferedWriter.Flush(); err != nil {
		_ = socket.Close()
		return nil, err
	}

	return &webSocketConnection{socket: socket}, nil
}

func isWebSocketOriginAllowed(request *http.Request) bool {
	originText := strings.TrimSpace(request.Header.Get("Origin"))
	if originText == "" {
		return true
	}

	originURL, err := url.Parse(originText)
	if err != nil {
		return false
	}
	if originURL.Host == "" || request.Host == "" {
		return false
	}
	return strings.EqualFold(originURL.Host, request.Host)
}

func buildWebSocketAcceptValue(websocketKey string) string {
	const websocketAcceptMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	acceptHash := sha1.Sum([]byte(websocketKey + websocketAcceptMagic))
	return base64.StdEncoding.EncodeToString(acceptHash[:])
}

func (connection *webSocketConnection) Close() error {
	return connection.socket.Close()
}

func (connection *webSocketConnection) WriteState(state appState) error {
	encodedState, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return connection.writeTextFrame(encodedState)
}

func (connection *webSocketConnection) ReadJSON(target any) error {
	readPayload, err := connection.readClientTextFrame()
	if err != nil {
		return err
	}
	return json.Unmarshal(readPayload, target)
}

func (connection *webSocketConnection) writeTextFrame(payload []byte) error {
	frameHeader := []byte{0x81}
	payloadLength := len(payload)
	if payloadLength <= 125 {
		frameHeader = append(frameHeader, byte(payloadLength))
	} else if payloadLength <= 65535 {
		frameHeader = append(frameHeader, 126, byte(payloadLength>>8), byte(payloadLength))
	} else {
		frameHeader = append(frameHeader, 127,
			byte(payloadLength>>56), byte(payloadLength>>48), byte(payloadLength>>40), byte(payloadLength>>32),
			byte(payloadLength>>24), byte(payloadLength>>16), byte(payloadLength>>8), byte(payloadLength))
	}
	if err := connection.socket.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := connection.socket.Write(frameHeader); err != nil {
		return err
	}
	if _, err := connection.socket.Write(payload); err != nil {
		return err
	}
	return nil
}

func (connection *webSocketConnection) readClientTextFrame() ([]byte, error) {
	frameHeader := make([]byte, 2)
	if _, err := io.ReadFull(connection.socket, frameHeader); err != nil {
		return nil, err
	}

	opcode := frameHeader[0] & 0x0F
	isMasked := frameHeader[1]&0x80 != 0
	if !isMasked {
		return nil, errors.New("client frame must be masked")
	}

	payloadLength := int(frameHeader[1] & 0x7F)
	switch payloadLength {
	case 126:
		extendedLength := make([]byte, 2)
		if _, err := io.ReadFull(connection.socket, extendedLength); err != nil {
			return nil, err
		}
		payloadLength = int(binary.BigEndian.Uint16(extendedLength))
	case 127:
		extendedLength := make([]byte, 8)
		if _, err := io.ReadFull(connection.socket, extendedLength); err != nil {
			return nil, err
		}
		parsedLength := binary.BigEndian.Uint64(extendedLength)
		if parsedLength > 8*1024*1024 {
			return nil, errors.New("frame too large")
		}
		payloadLength = int(parsedLength)
	}

	maskingKey := make([]byte, 4)
	if _, err := io.ReadFull(connection.socket, maskingKey); err != nil {
		return nil, err
	}
	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(connection.socket, payload); err != nil {
		return nil, err
	}
	for index := range payload {
		payload[index] ^= maskingKey[index%4]
	}

	switch opcode {
	case 0x1:
		return payload, nil
	case 0x8:
		return nil, io.EOF
	default:
		return nil, errors.New("unsupported opcode")
	}
}

func handleControlWebSocket(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		channelText := strings.TrimSpace(request.URL.Query().Get("channel"))
		channelNumber, err := strconv.Atoi(channelText)
		if err != nil || channelNumber < 1 || channelNumber > outputCount {
			http.Error(writer, "invalid channel", http.StatusBadRequest)
			return
		}

		websocketConnection, err := upgradeToWebSocket(writer, request)
		if err != nil {
			http.Error(writer, "websocket upgrade failed", http.StatusBadRequest)
			return
		}
		defer websocketConnection.Close()

		for {
			var controlRequest controlWebSocketRequest
			if err := websocketConnection.ReadJSON(&controlRequest); err != nil {
				return
			}

			controlReply := stateReply{state: requestStateSnapshot(stateCommands).state}
			switch controlRequest.Kind {
			case "power":
				reply := make(chan stateReply, 1)
				stateCommands <- stateCommand{kind: "set_output_power", channel: channelNumber, power: controlRequest.Power, reply: reply}
				controlReply = <-reply
			case "pwm":
				reply := make(chan stateReply, 1)
				stateCommands <- stateCommand{kind: "set_output_pwm", channel: channelNumber, pwm: controlRequest.PWM, reply: reply}
				controlReply = <-reply
			default:
				controlReply.err = errors.New("unknown control kind")
			}

			controlResponse := controlWebSocketResponse{
				RequestID: controlRequest.RequestID,
				OK:        controlReply.err == nil,
				Channel:   channelNumber,
				Kind:      controlRequest.Kind,
				State:     controlReply.state,
			}
			if controlReply.err != nil {
				controlResponse.Error = controlReply.err.Error()
			}
			encodedResponse, err := json.Marshal(controlResponse)
			if err != nil {
				return
			}
			if err := websocketConnection.writeTextFrame(encodedResponse); err != nil {
				return
			}
		}
	}
}

func requestStateSnapshot(stateCommands chan<- stateCommand) stateReply {
	reply := make(chan stateReply, 1)
	stateCommands <- stateCommand{kind: "get", reply: reply}
	return <-reply
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

func handleSetDLLOverride(stateCommands chan<- stateCommand) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := request.ParseMultipartForm(24 << 20); err != nil {
			http.Error(writer, "invalid multipart form", http.StatusBadRequest)
			return
		}
		uploadedFile, uploadedHeader, err := request.FormFile("dll_file")
		if err != nil {
			http.Error(writer, "missing dll file", http.StatusBadRequest)
			return
		}
		defer uploadedFile.Close()

		persistedDLLPath, err := saveUploadedDLLFile(uploadedFile, uploadedHeader.Filename)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}

		reply := make(chan stateReply, 1)
		stateCommands <- stateCommand{kind: "set_dll_override", label: persistedDLLPath, reply: reply}

		result := <-reply
		if result.err != nil {
			http.Error(writer, result.err.Error(), http.StatusBadRequest)
			return
		}

		writeJSON(writer, result.state)
	}
}

func saveUploadedDLLFile(uploadedDLL io.Reader, originalFilename string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", errors.New("DLL override is available only on Windows")
	}

	sanitizedFilename := strings.TrimSpace(filepath.Base(originalFilename))
	if sanitizedFilename == "" {
		return "", errors.New("dll filename is required")
	}
	if !strings.EqualFold(filepath.Ext(sanitizedFilename), ".dll") {
		return "", errors.New("uploaded file must have .dll extension")
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve working directory: %w", err)
	}
	overrideDirectory := filepath.Join(workingDirectory, "dll-overrides")
	if err := os.MkdirAll(overrideDirectory, 0o755); err != nil {
		return "", fmt.Errorf("failed to create override directory: %w", err)
	}

	destinationPath := filepath.Join(overrideDirectory, sanitizedFilename)
	destinationFile, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to create destination DLL: %w", err)
	}
	defer destinationFile.Close()

	if _, err := io.Copy(destinationFile, uploadedDLL); err != nil {
		return "", fmt.Errorf("failed to save uploaded DLL: %w", err)
	}
	return destinationPath, nil
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
