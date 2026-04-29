package guiding

import (
	"math"
	"strconv"
)

const (
	defaultManualHintDeadbandPixels = 0.7
	defaultManualHintMinimumPulseMs = 20
	defaultManualHintMaximumPulseMs = 2500
)

type ManualCorrectionAdvice struct {
	ShouldAct      bool   `json:"should_act"`
	AxisXDirection string `json:"axis_x_direction"`
	AxisXPulseMs   int    `json:"axis_x_pulse_ms"`
	AxisYDirection string `json:"axis_y_direction"`
	AxisYPulseMs   int    `json:"axis_y_pulse_ms"`
	Summary        string `json:"summary"`
}

// BuildManualCorrectionAdvice converts measured drift into operator-facing pulse instructions.
// We keep thresholds conservative to avoid over-correcting while the loop is still manual.
func BuildManualCorrectionAdvice(deltaX float64, deltaY float64, motorXMs float64, motorYMs float64) ManualCorrectionAdvice {
	if math.Abs(deltaX) <= defaultManualHintDeadbandPixels && math.Abs(deltaY) <= defaultManualHintDeadbandPixels {
		return ManualCorrectionAdvice{
			ShouldAct: false,
			Summary:   "Drift is inside deadband. Keep tracking and do not press correction keys.",
		}
	}

	axisXDirection, axisXPulse := buildManualAxisHint("X", motorXMs)
	axisYDirection, axisYPulse := buildManualAxisHint("Y", motorYMs)

	if axisXPulse == 0 && axisYPulse == 0 {
		return ManualCorrectionAdvice{
			ShouldAct: false,
			Summary:   "Correction pulse is below minimum threshold. Wait for the next frame.",
		}
	}

	return ManualCorrectionAdvice{
		ShouldAct:      true,
		AxisXDirection: axisXDirection,
		AxisXPulseMs:   axisXPulse,
		AxisYDirection: axisYDirection,
		AxisYPulseMs:   axisYPulse,
		Summary:        buildManualHintSummary(axisXDirection, axisXPulse, axisYDirection, axisYPulse),
	}
}

func buildManualAxisHint(axisName string, signedPulseMs float64) (string, int) {
	roundedPulse := int(math.Round(math.Abs(signedPulseMs)))
	if roundedPulse < defaultManualHintMinimumPulseMs {
		return "", 0
	}
	if roundedPulse > defaultManualHintMaximumPulseMs {
		roundedPulse = defaultManualHintMaximumPulseMs
	}

	directionSign := "+"
	if signedPulseMs < 0 {
		directionSign = "-"
	}
	return directionSign + axisName, roundedPulse
}

func buildManualHintSummary(axisXDirection string, axisXPulse int, axisYDirection string, axisYPulse int) string {
	if axisXPulse == 0 {
		return "Press " + axisYDirection + " for " + formatPulseDuration(axisYPulse) + "."
	}
	if axisYPulse == 0 {
		return "Press " + axisXDirection + " for " + formatPulseDuration(axisXPulse) + "."
	}
	return "Press " + axisXDirection + " for " + formatPulseDuration(axisXPulse) + " and " + axisYDirection + " for " + formatPulseDuration(axisYPulse) + "."
}

func formatPulseDuration(pulseMs int) string {
	return strconv.Itoa(pulseMs) + " ms"
}
