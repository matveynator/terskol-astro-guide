//go:build !windows

package gpio

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

type fileAdapter struct {
	inputTemplate  string
	outputTemplate string
}

func DefaultInputTemplate() string {
	if runtime.GOOS == "darwin" {
		return "/tmp/astro-control/di%d.value"
	}
	return "/sys/class/gpio/gpio%d/value"
}

func DefaultOutputTemplate() string {
	if runtime.GOOS == "darwin" {
		return "/tmp/astro-control/do%d.value"
	}
	return "/sys/class/gpio/gpio%d/value"
}

func Open(config Config) (Adapter, RuntimeMode, error) {
	adapter := &fileAdapter{
		inputTemplate:  strings.TrimSpace(config.InputTemplate),
		outputTemplate: strings.TrimSpace(config.OutputTemplate),
	}
	if adapter.inputTemplate == "" {
		adapter.inputTemplate = DefaultInputTemplate()
	}
	if adapter.outputTemplate == "" {
		adapter.outputTemplate = DefaultOutputTemplate()
	}

	mode := RuntimeMode{}
	if firstMissingPath(adapter.inputTemplate, InputCount) != "" {
		mode.InputSimulation = true
	}
	if firstMissingPath(adapter.outputTemplate, OutputCount) != "" {
		mode.OutputSimulation = true
	}
	if mode.InputSimulation && mode.OutputSimulation {
		return SimulationAdapter{}, mode, nil
	}
	return adapter, mode, nil
}

func (adapter *fileAdapter) ReadInput(channel int) (bool, error) {
	if channel < 1 || channel > InputCount {
		return false, fmt.Errorf("invalid input channel %d", channel)
	}
	if firstMissingPath(adapter.inputTemplate, InputCount) != "" {
		return false, os.ErrNotExist
	}

	inputPath := fmt.Sprintf(adapter.inputTemplate, channel)
	rawSignal, err := os.ReadFile(inputPath)
	if err != nil {
		return false, err
	}

	trimmedSignal := strings.TrimSpace(strings.ToLower(string(rawSignal)))
	switch trimmedSignal {
	case "1", "on", "high", "true":
		return true, nil
	default:
		return false, nil
	}
}

func (adapter *fileAdapter) WriteOutput(channel int, high bool) error {
	if channel < 1 || channel > OutputCount {
		return fmt.Errorf("invalid output channel %d", channel)
	}
	if firstMissingPath(adapter.outputTemplate, OutputCount) != "" {
		return os.ErrNotExist
	}

	nextValue := "0"
	if high {
		nextValue = "1"
	}
	outputPath := fmt.Sprintf(adapter.outputTemplate, channel)
	if err := os.WriteFile(outputPath, []byte(nextValue), 0o644); err != nil {
		return fmt.Errorf("write output %q: %w", outputPath, err)
	}
	return nil
}

func (adapter *fileAdapter) Close() error { return nil }

func firstMissingPath(pathTemplate string, channelCount int) string {
	for channel := 1; channel <= channelCount; channel++ {
		channelPath := fmt.Sprintf(pathTemplate, channel)
		if _, err := os.Stat(channelPath); err != nil {
			if os.IsNotExist(err) {
				return channelPath
			}
		}
	}
	return ""
}
