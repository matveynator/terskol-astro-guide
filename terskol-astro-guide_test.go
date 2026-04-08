package main

import "testing"

func TestParseInputVoltageAndSignalNumericValuesUseThresholdPath(t *testing.T) {
	config := runtimeConfig{
		inputOnVoltage:        24,
		inputOffVoltage:       0,
		inputThresholdVoltage: 2,
	}

	voltage, signal := parseInputVoltageAndSignal("1", config)
	if voltage != 1 {
		t.Fatalf("expected voltage 1, got %v", voltage)
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
