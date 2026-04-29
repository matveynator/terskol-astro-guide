package guiding

import (
	"image"
	"testing"
)

func TestLiveTrackerSessionLifecycle(t *testing.T) {
	tracker := StartLiveTracker()

	baseStars := []vector2{{x: 24, y: 26}, {x: 63, y: 43}, {x: 98, y: 70}, {x: 46, y: 95}, {x: 104, y: 111}, {x: 132, y: 125}}
	referenceFrame := buildSyntheticGuideFrame(160, 140, baseStars, vector2{x: 0, y: 0}, 0)
	firstLiveFrame := buildSyntheticGuideFrame(160, 140, baseStars, vector2{x: 2.1, y: -1.4}, 0.2)
	secondLiveFrame := buildSyntheticGuideFrame(160, 140, baseStars, vector2{x: 3.8, y: -2.0}, 0.2)

	startSnapshot, startError := tracker.StartSession(LiveTrackerSessionConfig{
		ReferenceFrame: referenceFrame,
		MaxStars:       40,
		PixelToMotor:   PixelToMotorMatrix{A: 40.0, D: 40.0},
	})
	if startError != nil {
		t.Fatalf("expected no start error, got %v", startError)
	}
	if !startSnapshot.SessionActive {
		t.Fatalf("expected active session after start")
	}
	if startSnapshot.ProcessedFrames != 0 {
		t.Fatalf("expected no processed frames right after start")
	}

	firstSnapshot, firstError := tracker.AnalyzeFrame(firstLiveFrame)
	if firstError != nil {
		t.Fatalf("expected first live frame to be solved, got %v", firstError)
	}
	if firstSnapshot.ProcessedFrames != 1 || firstSnapshot.SuccessfulFrames != 1 {
		t.Fatalf("expected counters (processed=1, successful=1), got processed=%d successful=%d", firstSnapshot.ProcessedFrames, firstSnapshot.SuccessfulFrames)
	}
	if firstSnapshot.LastResult.Confidence < 0.4 {
		t.Fatalf("expected confidence >= 0.4, got %f", firstSnapshot.LastResult.Confidence)
	}
	if !firstSnapshot.OperatorHint.ShouldAct {
		t.Fatalf("expected operator hint to request manual action")
	}

	secondSnapshot, secondError := tracker.AnalyzeFrame(secondLiveFrame)
	if secondError != nil {
		t.Fatalf("expected second live frame to be solved, got %v", secondError)
	}
	if secondSnapshot.ProcessedFrames != 2 || secondSnapshot.SuccessfulFrames != 2 {
		t.Fatalf("expected counters (processed=2, successful=2), got processed=%d successful=%d", secondSnapshot.ProcessedFrames, secondSnapshot.SuccessfulFrames)
	}
	if secondSnapshot.LastResult.FrameIndex != 2 {
		t.Fatalf("expected last frame index 2, got %d", secondSnapshot.LastResult.FrameIndex)
	}
	if secondSnapshot.OperatorHint.Summary == "" {
		t.Fatalf("expected operator summary to be non-empty")
	}
}

func TestLiveTrackerFrameBeforeStartReturnsError(t *testing.T) {
	tracker := StartLiveTracker()
	frame := image.NewRGBA(image.Rect(0, 0, 80, 80))

	snapshot, analyzeError := tracker.AnalyzeFrame(frame)
	if analyzeError == nil {
		t.Fatalf("expected error before tracker session start")
	}
	if snapshot.ProcessedFrames != 0 {
		t.Fatalf("expected processed frames to stay zero, got %d", snapshot.ProcessedFrames)
	}
}
