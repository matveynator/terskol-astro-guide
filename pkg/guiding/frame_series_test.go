package guiding

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestAnalyzeFrameSeriesBuildsDriftTimeline(t *testing.T) {
	frameWidth := 200
	frameHeight := 160
	baseStars := []vector2{
		{x: 22, y: 28},
		{x: 48, y: 42},
		{x: 88, y: 50},
		{x: 132, y: 67},
		{x: 171, y: 80},
		{x: 58, y: 111},
		{x: 96, y: 129},
		{x: 140, y: 139},
	}

	referenceFrame := buildSyntheticGuideFrame(frameWidth, frameHeight, baseStars, vector2{x: 0, y: 0}, 0)
	secondFrame := buildSyntheticGuideFrame(frameWidth, frameHeight, baseStars, vector2{x: 2.5, y: -1.0}, 0.15)
	thirdFrame := buildSyntheticGuideFrame(frameWidth, frameHeight, baseStars, vector2{x: 5.0, y: -2.2}, 0.25)
	fourthFrame := buildSyntheticGuideFrame(frameWidth, frameHeight, baseStars, vector2{x: 7.4, y: -3.0}, 0.35)

	seriesResult := AnalyzeFrameSeries(FrameSeriesRequest{
		Frames:       []image.Image{referenceFrame, secondFrame, thirdFrame, fourthFrame},
		MaxStars:     48,
		PixelToMotor: PixelToMotorMatrix{A: 5.0, B: 0.0, C: 0.0, D: 5.0},
	})

	if seriesResult.TotalFrames != 4 {
		t.Fatalf("expected 4 total frames, got %d", seriesResult.TotalFrames)
	}
	if seriesResult.SolvedFrames != 3 {
		t.Fatalf("expected 3 solved frames, got %d", seriesResult.SolvedFrames)
	}
	if len(seriesResult.Points) != 3 {
		t.Fatalf("expected 3 timeline points, got %d", len(seriesResult.Points))
	}

	if seriesResult.Points[0].FrameIndex != 1 || seriesResult.Points[1].FrameIndex != 2 || seriesResult.Points[2].FrameIndex != 3 {
		t.Fatalf("expected ordered frame indexes [1,2,3], got [%d,%d,%d]", seriesResult.Points[0].FrameIndex, seriesResult.Points[1].FrameIndex, seriesResult.Points[2].FrameIndex)
	}

	if seriesResult.Points[0].DeltaX >= seriesResult.Points[1].DeltaX || seriesResult.Points[1].DeltaX >= seriesResult.Points[2].DeltaX {
		t.Fatalf("expected monotonic x drift growth, got [%f,%f,%f]", seriesResult.Points[0].DeltaX, seriesResult.Points[1].DeltaX, seriesResult.Points[2].DeltaX)
	}

	if math.Abs(seriesResult.Points[2].DeltaY-(-3.0)) > 1.1 {
		t.Fatalf("expected final dy around -3.0, got %f", seriesResult.Points[2].DeltaY)
	}
	if seriesResult.Points[2].Confidence < 0.55 {
		t.Fatalf("expected confidence >= 0.55, got %f", seriesResult.Points[2].Confidence)
	}
}

func TestAnalyzeFrameSeriesKeepsErrorsPerFrame(t *testing.T) {
	baseStars := []vector2{{x: 30, y: 30}, {x: 70, y: 45}, {x: 100, y: 80}, {x: 44, y: 100}, {x: 90, y: 120}}
	referenceFrame := buildSyntheticGuideFrame(140, 140, baseStars, vector2{x: 0, y: 0}, 0)
	validFrame := buildSyntheticGuideFrame(140, 140, baseStars, vector2{x: 1.4, y: 0.8}, 0)
	invalidSizeFrame := buildSyntheticGuideFrame(100, 100, baseStars, vector2{x: 1.4, y: 0.8}, 0)

	seriesResult := AnalyzeFrameSeries(FrameSeriesRequest{Frames: []image.Image{referenceFrame, validFrame, invalidSizeFrame}, MaxStars: 24})
	if seriesResult.SolvedFrames != 1 {
		t.Fatalf("expected one solved frame, got %d", seriesResult.SolvedFrames)
	}
	if seriesResult.FailedFrames != 1 {
		t.Fatalf("expected one failed frame, got %d", seriesResult.FailedFrames)
	}
	if seriesResult.Points[1].Error == "" {
		t.Fatalf("expected explicit error for invalid frame size")
	}
}

func buildSyntheticGuideFrame(frameWidth int, frameHeight int, baseStars []vector2, translation vector2, rotationDeg float64) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, frameWidth, frameHeight))
	fillFrame(frame, color.RGBA{R: 3, G: 3, B: 3, A: 255})
	rotationRadians := rotationDeg * math.Pi / 180
	frameCenter := vector2{x: float64(frameWidth) / 2, y: float64(frameHeight) / 2}
	for _, baseStar := range baseStars {
		offsetFromCenter := vectorFromPoints(frameCenter, baseStar)
		rotatedOffset := rotateVector(offsetFromCenter, rotationRadians)
		starPosition := vector2{
			x: frameCenter.x + rotatedOffset.x + translation.x,
			y: frameCenter.y + rotatedOffset.y + translation.y,
		}
		placeStar(frame, int(math.Round(starPosition.x)), int(math.Round(starPosition.y)), 245)
	}
	return frame
}
