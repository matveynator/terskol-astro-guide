package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestParseInputVoltageAndSignalDigitalTokensUseDigitalMapping(t *testing.T) {
	config := runtimeConfig{
		inputOnVoltage:        24,
		inputOffVoltage:       -1,
		inputThresholdVoltage: 2,
	}

	voltage, signal := parseInputVoltageAndSignal("1", config)
	if voltage != 24 {
		t.Fatalf("expected voltage 24, got %v", voltage)
	}
	if signal != "on" {
		t.Fatalf("expected signal on, got %q", signal)
	}

	voltage, signal = parseInputVoltageAndSignal("0", config)
	if voltage != -1 {
		t.Fatalf("expected voltage -1, got %v", voltage)
	}
	if signal != "off" {
		t.Fatalf("expected signal off, got %q", signal)
	}
}

func TestParseInputVoltageAndSignalNumericValuesUseThresholdPath(t *testing.T) {
	config := runtimeConfig{
		inputOnVoltage:        24,
		inputOffVoltage:       0,
		inputThresholdVoltage: 2,
	}

	voltage, signal := parseInputVoltageAndSignal("1.5", config)
	if voltage != 1.5 {
		t.Fatalf("expected voltage 1.5, got %v", voltage)
	}
	if signal != "off" {
		t.Fatalf("expected signal off, got %q", signal)
	}
}

func TestParseInputVoltageAndSignalFallsBackToDigitalMappingForNonNumericPayloads(t *testing.T) {
	config := runtimeConfig{
		inputOnVoltage:        24,
		inputOffVoltage:       0,
		inputThresholdVoltage: 2,
	}

	voltage, signal := parseInputVoltageAndSignal("not-a-number", config)
	if voltage != 0 {
		t.Fatalf("expected voltage 0, got %v", voltage)
	}
	if signal != "off" {
		t.Fatalf("expected signal off, got %q", signal)
	}
}

func TestBuildInitialStateUsesPhysicalPinLabelsByDefault(t *testing.T) {
	state := buildInitialState(map[string]string{}, map[int]savedOutputState{})

	if state.Inputs[0].Label != "DI1" || state.Inputs[7].Label != "DI8" {
		t.Fatalf("unexpected default DI labels: first=%q last=%q", state.Inputs[0].Label, state.Inputs[7].Label)
	}

	if state.Outputs[0].Label != "DO11" || state.Outputs[7].Label != "DO18" {
		t.Fatalf("unexpected default DO labels: first=%q last=%q", state.Outputs[0].Label, state.Outputs[7].Label)
	}
}

func TestApplyOutputPowerKeepsPWMValueWhenDisabled(t *testing.T) {
	initialState := appState{
		Outputs: []outputState{{Channel: 1, Power: "on", PWM: 37, Label: "DO11"}},
	}

	nextState, err := applyOutputPower(initialState, 1, "off")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if nextState.Outputs[0].PWM != 37 {
		t.Fatalf("expected pwm to remain 37, got %d", nextState.Outputs[0].PWM)
	}
}

func TestApplyLabelUsesDefaultPinLabelWhenInputIsBlank(t *testing.T) {
	initialState := appState{
		Inputs:  []inputState{{Channel: 1, Label: "MySensor"}},
		Outputs: []outputState{{Channel: 1, Label: "MyOutput"}},
	}

	nextInputState, inputErr := applyLabel(initialState, "input", 1, "")
	if inputErr != nil {
		t.Fatalf("expected no error for empty input label, got %v", inputErr)
	}
	if nextInputState.Inputs[0].Label != "DI1" {
		t.Fatalf("expected input label DI1, got %q", nextInputState.Inputs[0].Label)
	}

	nextOutputState, outputErr := applyLabel(initialState, "output", 1, "   ")
	if outputErr != nil {
		t.Fatalf("expected no error for empty output label, got %v", outputErr)
	}
	if nextOutputState.Outputs[0].Label != "DO11" {
		t.Fatalf("expected output label DO11, got %q", nextOutputState.Outputs[0].Label)
	}
}

func TestBuildInitialStateNormalizesLegacyPortLabelsToRealPins(t *testing.T) {
	legacyLabels := map[string]string{
		"input-7":  "DI6",
		"output-8": "DO7",
	}

	state := buildInitialState(legacyLabels, map[int]savedOutputState{})
	if state.Inputs[6].Label != "DI7" {
		t.Fatalf("expected DI7 for input channel 7, got %q", state.Inputs[6].Label)
	}
	if state.Outputs[7].Label != "DO18" {
		t.Fatalf("expected DO18 for output channel 8, got %q", state.Outputs[7].Label)
	}
}

func TestApplyOutputPWMDoesNotTogglePowerState(t *testing.T) {
	offState := appState{
		Outputs: []outputState{{Channel: 1, Power: "off", PWM: 0, Label: "DO11"}},
	}
	offUpdatedState, offErr := applyOutputPWM(offState, 1, 35)
	if offErr != nil {
		t.Fatalf("expected no error for off state pwm update, got %v", offErr)
	}
	if offUpdatedState.Outputs[0].Power != "off" {
		t.Fatalf("expected off state power to stay off, got %q", offUpdatedState.Outputs[0].Power)
	}
	if offUpdatedState.Outputs[0].PWM != 35 {
		t.Fatalf("expected off state pwm 35, got %d", offUpdatedState.Outputs[0].PWM)
	}

	onState := appState{
		Outputs: []outputState{{Channel: 1, Power: "on", PWM: 20, Label: "DO11"}},
	}
	onUpdatedState, onErr := applyOutputPWM(onState, 1, 55)
	if onErr != nil {
		t.Fatalf("expected no error for on state pwm update, got %v", onErr)
	}
	if onUpdatedState.Outputs[0].Power != "on" {
		t.Fatalf("expected on state power to stay on, got %q", onUpdatedState.Outputs[0].Power)
	}
	if onUpdatedState.Outputs[0].PWM != 55 {
		t.Fatalf("expected on state pwm 55, got %d", onUpdatedState.Outputs[0].PWM)
	}
}

func TestResolveSettingsFilePathCreatesExplicitSettingsFile(t *testing.T) {
	temporaryDirectory := t.TempDir()
	settingsFile := filepath.Join(temporaryDirectory, "custom-settings.json")

	resolvedSettingsFile, resolveError := resolveSettingsFilePath(settingsFile)
	if resolveError != nil {
		t.Fatalf("expected no resolve error, got %v", resolveError)
	}
	if resolvedSettingsFile != settingsFile {
		t.Fatalf("expected settings path %q, got %q", settingsFile, resolvedSettingsFile)
	}

	loadedSettings := loadSettings(settingsFile)
	if len(loadedSettings.Labels) != 0 {
		t.Fatalf("expected empty labels map, got %v", loadedSettings.Labels)
	}
	if len(loadedSettings.Outputs) != 0 {
		t.Fatalf("expected empty outputs list, got %v", loadedSettings.Outputs)
	}
}

func TestResolveSettingsFilePathUsesOSSpecificDefaultLocation(t *testing.T) {
	resolvedSettingsFile, resolveError := resolveSettingsFilePath("")
	if resolveError != nil {
		t.Fatalf("expected no resolve error, got %v", resolveError)
	}

	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(strings.ToLower(resolvedSettingsFile), "c:\\") && !strings.HasPrefix(strings.ToLower(resolvedSettingsFile), "c:/") {
			t.Fatalf("expected Windows settings file on C drive, got %q", resolvedSettingsFile)
		}
		return
	}

	homeDirectory, homeError := os.UserHomeDir()
	if homeError != nil {
		t.Fatalf("expected readable home directory, got %v", homeError)
	}
	if !strings.HasPrefix(resolvedSettingsFile, homeDirectory) {
		t.Fatalf("expected settings file in home directory %q, got %q", homeDirectory, resolvedSettingsFile)
	}
	if !strings.HasPrefix(filepath.Base(resolvedSettingsFile), ".") {
		t.Fatalf("expected hidden dotfile for unix-like OS, got %q", resolvedSettingsFile)
	}
	if !strings.HasSuffix(resolvedSettingsFile, ".conf") {
		t.Fatalf("expected .conf settings file extension, got %q", resolvedSettingsFile)
	}
}

func TestDetectIORuntimeModeEnablesSimulationWhenPathsMissing(t *testing.T) {
	temporaryDirectory := t.TempDir()
	resolvedPaths := ioPaths{
		inputTemplate:  filepath.Join(temporaryDirectory, "missing-di-%d.value"),
		outputTemplate: filepath.Join(temporaryDirectory, "missing-do-%d.value"),
	}

	mode := detectIORuntimeMode(resolvedPaths)
	if !mode.inputSimulation {
		t.Fatalf("expected input simulation mode to be enabled")
	}
	if !mode.outputSimulation {
		t.Fatalf("expected output simulation mode to be enabled")
	}
	if !mode.state.TestMode {
		t.Fatalf("expected runtime state to mark test mode")
	}
	if mode.state.Message == "" {
		t.Fatalf("expected runtime state message to explain missing GPIO paths")
	}
}

func TestFirstMissingChannelPathWithStatIgnoresPermissionErrors(t *testing.T) {
	statPath := func(path string) (os.FileInfo, error) {
		if strings.HasSuffix(path, strconv.Itoa(1)) {
			return nil, os.ErrPermission
		}
		return nil, errors.New("transient io error")
	}

	missingPath := firstMissingChannelPathWithStat("channel-%d", 2, statPath)
	if missingPath != "" {
		t.Fatalf("expected no missing path fallback on permission/transient errors, got %q", missingPath)
	}
}

func TestFirstMissingChannelPathWithStatReturnsMissingPathForNotExistError(t *testing.T) {
	statPath := func(path string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}

	missingPath := firstMissingChannelPathWithStat("channel-%d", 4, statPath)
	if missingPath != "channel-1" {
		t.Fatalf("expected first missing channel path, got %q", missingPath)
	}
}
