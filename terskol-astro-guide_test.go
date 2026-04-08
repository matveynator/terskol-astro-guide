package main

import "testing"

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
