package gpio

import (
	"errors"
	"fmt"
)

const (
	InputCount  = 8
	OutputCount = 8
)

type RuntimeMode struct {
	InputSimulation  bool
	OutputSimulation bool
	ActiveDriver     string
	DriverProbeLog   string
}

type Adapter interface {
	ReadInput(channel int) (bool, error)
	WriteOutput(channel int, high bool) error
	Close() error
}

type Config struct {
	InputTemplate  string
	OutputTemplate string
	WindowsDLLPath string
}

type SimulationAdapter struct{}

type probeLogCarrier interface {
	ProbeLog() string
}

func ProbeLogFromError(err error) string {
	if err == nil {
		return ""
	}

	var carrier probeLogCarrier
	if errors.As(err, &carrier) {
		return carrier.ProbeLog()
	}
	return err.Error()
}

func (SimulationAdapter) ReadInput(channel int) (bool, error) {
	if channel < 1 || channel > InputCount {
		return false, fmt.Errorf("invalid input channel %d", channel)
	}
	return false, nil
}

func (SimulationAdapter) WriteOutput(channel int, high bool) error {
	if channel < 1 || channel > OutputCount {
		return fmt.Errorf("invalid output channel %d", channel)
	}
	_ = high
	return nil
}

func (SimulationAdapter) Close() error { return nil }
