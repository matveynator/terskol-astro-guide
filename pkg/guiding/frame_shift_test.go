package guiding

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestAnalyzeFrameShiftReturnsTranslationRotationAndConfidence(t *testing.T) {
	referenceFrame := image.NewRGBA(image.Rect(0, 0, 220, 180))
	currentFrame := image.NewRGBA(image.Rect(0, 0, 220, 180))
	fillFrame(referenceFrame, color.RGBA{R: 2, G: 2, B: 2, A: 255})
	fillFrame(currentFrame, color.RGBA{R: 2, G: 2, B: 2, A: 255})

	baseStars := []vector2{
		{x: 38, y: 42},
		{x: 78, y: 55},
		{x: 145, y: 64},
		{x: 184, y: 99},
		{x: 62, y: 123},
		{x: 128, y: 134},
		{x: 171, y: 149},
	}
	for _, star := range baseStars {
		placeStar(referenceFrame, int(star.x), int(star.y), 255)
	}

	translationX := 8.2
	translationY := -3.6
	rotationDegrees := 0.6
	rotationRadians := rotationDegrees * math.Pi / 180
	center := vector2{x: 110, y: 90}
	for _, star := range baseStars {
		offset := vectorFromPoints(center, star)
		rotated := rotateVector(offset, rotationRadians)
		moved := vector2{x: center.x + rotated.x + translationX, y: center.y + rotated.y + translationY}
		placeStar(currentFrame, int(math.Round(moved.x)), int(math.Round(moved.y)), 250)
	}

	result, err := AnalyzeFrameShift(FrameShiftRequest{
		ReferenceFrame: referenceFrame,
		CurrentFrame:   currentFrame,
		MaxStars:       40,
		PixelToMotor:   PixelToMotorMatrix{A: 6.0, B: 0.0, C: 0.0, D: 6.0},
	})
	if err != nil {
		t.Fatalf("expected frame shift solve success, got %v", err)
	}
	if result.MatchedStars < 5 {
		t.Fatalf("expected at least five matched stars, got %d", result.MatchedStars)
	}
	if math.Abs(result.DeltaX-translationX) > 1.2 {
		t.Fatalf("expected dx around %f, got %f", translationX, result.DeltaX)
	}
	if math.Abs(result.DeltaY-translationY) > 1.2 {
		t.Fatalf("expected dy around %f, got %f", translationY, result.DeltaY)
	}
	if math.Abs(result.RotationDeg-rotationDegrees) > 0.8 {
		t.Fatalf("expected rotation around %f deg, got %f", rotationDegrees, result.RotationDeg)
	}
	if result.Confidence < 0.6 {
		t.Fatalf("expected confidence >= 0.6, got %f", result.Confidence)
	}
	if result.SuggestedMotor.MotorXMs >= 0 {
		t.Fatalf("expected negative motor X correction for positive drift, got %f", result.SuggestedMotor.MotorXMs)
	}
}

func TestAnalyzeFrameShiftReturnsErrorWhenTooFewStars(t *testing.T) {
	referenceFrame := image.NewRGBA(image.Rect(0, 0, 90, 70))
	currentFrame := image.NewRGBA(image.Rect(0, 0, 90, 70))
	fillFrame(referenceFrame, color.RGBA{R: 1, G: 1, B: 1, A: 255})
	fillFrame(currentFrame, color.RGBA{R: 1, G: 1, B: 1, A: 255})

	placeStar(referenceFrame, 20, 20, 255)
	placeStar(referenceFrame, 60, 40, 240)
	placeStar(currentFrame, 22, 20, 255)
	placeStar(currentFrame, 62, 40, 240)

	_, err := AnalyzeFrameShift(FrameShiftRequest{ReferenceFrame: referenceFrame, CurrentFrame: currentFrame, MaxStars: 20})
	if err == nil {
		t.Fatalf("expected error when insufficient stars are available")
	}
}

func TestAnalyzeFrameShiftUsesDefaultStarLimitWhenRequestIsZero(t *testing.T) {
	referenceFrame := image.NewRGBA(image.Rect(0, 0, 260, 200))
	currentFrame := image.NewRGBA(image.Rect(0, 0, 260, 200))
	fillFrame(referenceFrame, color.RGBA{R: 3, G: 3, B: 3, A: 255})
	fillFrame(currentFrame, color.RGBA{R: 3, G: 3, B: 3, A: 255})

	for starIndex := 0; starIndex < 12; starIndex += 1 {
		x := 20 + (starIndex * 18)
		y := 28 + (starIndex * 11 % 130)
		placeStar(referenceFrame, x, y, 245)
		placeStar(currentFrame, x+3, y-2, 245)
	}

	result, err := AnalyzeFrameShift(FrameShiftRequest{
		ReferenceFrame: referenceFrame,
		CurrentFrame:   currentFrame,
		MaxStars:       0,
	})
	if err != nil {
		t.Fatalf("expected successful solve with default star limit, got %v", err)
	}
	if len(result.ReferenceGuideStars) < 8 {
		t.Fatalf("expected default star limit path to keep enough stars, got %d", len(result.ReferenceGuideStars))
	}
}
