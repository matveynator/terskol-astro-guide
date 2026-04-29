package guiding

import "testing"

func TestBuildManualCorrectionAdviceReturnsActionablePulseHints(t *testing.T) {
	advice := BuildManualCorrectionAdvice(4.2, -2.1, -115.3, 37.6)
	if !advice.ShouldAct {
		t.Fatalf("expected actionable hint")
	}
	if advice.AxisXDirection != "-X" || advice.AxisXPulseMs != 115 {
		t.Fatalf("unexpected axis X hint: %s %d", advice.AxisXDirection, advice.AxisXPulseMs)
	}
	if advice.AxisYDirection != "+Y" || advice.AxisYPulseMs != 38 {
		t.Fatalf("unexpected axis Y hint: %s %d", advice.AxisYDirection, advice.AxisYPulseMs)
	}
	if advice.Summary == "" {
		t.Fatalf("expected non-empty summary")
	}
}

func TestBuildManualCorrectionAdviceReturnsNoActionInsideDeadband(t *testing.T) {
	advice := BuildManualCorrectionAdvice(0.3, -0.4, 12, -8)
	if advice.ShouldAct {
		t.Fatalf("expected no action for deadband drift")
	}
	if advice.AxisXPulseMs != 0 || advice.AxisYPulseMs != 0 {
		t.Fatalf("expected zero pulses for deadband drift")
	}
}
