package guiding

import (
	"errors"
	"image"
	"math"
)

const (
	defaultSearchRadiusPixels = 42
	minSearchRadiusPixels     = 8
	maxSearchRadiusPixels     = 160
)

type AnalyzeRequest struct {
	SelectedX    float64
	SelectedY    float64
	SearchRadius int
}

type AnalyzeResult struct {
	FrameWidth      int     `json:"frame_width"`
	FrameHeight     int     `json:"frame_height"`
	TargetX         float64 `json:"target_x"`
	TargetY         float64 `json:"target_y"`
	FoundX          float64 `json:"found_x"`
	FoundY          float64 `json:"found_y"`
	OffsetX         float64 `json:"offset_x"`
	OffsetY         float64 `json:"offset_y"`
	CenterOffsetX   float64 `json:"center_offset_x"`
	CenterOffsetY   float64 `json:"center_offset_y"`
	Confidence      float64 `json:"confidence"`
	CorrectionHint  string  `json:"correction_hint"`
	TrackingSuccess bool    `json:"tracking_success"`
}

// AnalyzeFrame finds the brightest local star around the selected target point.
// The algorithm favors deterministic behavior over complexity to keep on-site debugging simple.
func AnalyzeFrame(frame image.Image, request AnalyzeRequest) (AnalyzeResult, error) {
	frameBounds := frame.Bounds()
	frameWidth := frameBounds.Dx()
	frameHeight := frameBounds.Dy()
	if frameWidth == 0 || frameHeight == 0 {
		return AnalyzeResult{}, errors.New("empty frame")
	}

	targetX := clampFloat64(request.SelectedX, 0, float64(frameWidth-1))
	targetY := clampFloat64(request.SelectedY, 0, float64(frameHeight-1))
	searchRadius := clampInt(request.SearchRadius, minSearchRadiusPixels, maxSearchRadiusPixels)
	if searchRadius == 0 {
		searchRadius = defaultSearchRadiusPixels
	}

	searchMinX := clampInt(int(math.Floor(targetX))-searchRadius, 0, frameWidth-1)
	searchMaxX := clampInt(int(math.Ceil(targetX))+searchRadius, 0, frameWidth-1)
	searchMinY := clampInt(int(math.Floor(targetY))-searchRadius, 0, frameHeight-1)
	searchMaxY := clampInt(int(math.Ceil(targetY))+searchRadius, 0, frameHeight-1)

	peakBrightness := -1.0
	peakX := int(math.Round(targetX))
	peakY := int(math.Round(targetY))
	backgroundBrightnessSum := 0.0
	backgroundSampleCount := 0.0

	for pixelY := searchMinY; pixelY <= searchMaxY; pixelY += 1 {
		for pixelX := searchMinX; pixelX <= searchMaxX; pixelX += 1 {
			pixelBrightness := grayBrightness(frame.At(pixelX+frameBounds.Min.X, pixelY+frameBounds.Min.Y))
			backgroundBrightnessSum += pixelBrightness
			backgroundSampleCount += 1.0
			if pixelBrightness > peakBrightness {
				peakBrightness = pixelBrightness
				peakX = pixelX
				peakY = pixelY
			}
		}
	}

	if peakBrightness < 0 {
		return AnalyzeResult{}, errors.New("failed to find peak brightness")
	}

	centroidX, centroidY, centroidWeight := computeLocalCentroid(frame, frameBounds, peakX, peakY)
	if centroidWeight <= 0 {
		centroidX = float64(peakX)
		centroidY = float64(peakY)
	}

	localAverageBrightness := 0.0
	if backgroundSampleCount > 0 {
		localAverageBrightness = backgroundBrightnessSum / backgroundSampleCount
	}

	confidence := 0.0
	if peakBrightness > 0 {
		confidence = clampFloat64((peakBrightness-localAverageBrightness)/peakBrightness, 0, 1)
	}

	offsetX := centroidX - targetX
	offsetY := centroidY - targetY
	frameCenterX := float64(frameWidth-1) / 2.0
	frameCenterY := float64(frameHeight-1) / 2.0
	centerOffsetX := centroidX - frameCenterX
	centerOffsetY := centroidY - frameCenterY

	return AnalyzeResult{
		FrameWidth:      frameWidth,
		FrameHeight:     frameHeight,
		TargetX:         targetX,
		TargetY:         targetY,
		FoundX:          centroidX,
		FoundY:          centroidY,
		OffsetX:         offsetX,
		OffsetY:         offsetY,
		CenterOffsetX:   centerOffsetX,
		CenterOffsetY:   centerOffsetY,
		Confidence:      confidence,
		CorrectionHint:  buildCorrectionHint(centerOffsetX, centerOffsetY),
		TrackingSuccess: confidence >= 0.15,
	}, nil
}

func computeLocalCentroid(frame image.Image, bounds image.Rectangle, peakX int, peakY int) (float64, float64, float64) {
	const centroidRadiusPixels = 4
	minX := clampInt(peakX-centroidRadiusPixels, 0, bounds.Dx()-1)
	maxX := clampInt(peakX+centroidRadiusPixels, 0, bounds.Dx()-1)
	minY := clampInt(peakY-centroidRadiusPixels, 0, bounds.Dy()-1)
	maxY := clampInt(peakY+centroidRadiusPixels, 0, bounds.Dy()-1)

	totalWeight := 0.0
	weightedX := 0.0
	weightedY := 0.0
	for pixelY := minY; pixelY <= maxY; pixelY += 1 {
		for pixelX := minX; pixelX <= maxX; pixelX += 1 {
			pixelBrightness := grayBrightness(frame.At(pixelX+bounds.Min.X, pixelY+bounds.Min.Y))
			totalWeight += pixelBrightness
			weightedX += float64(pixelX) * pixelBrightness
			weightedY += float64(pixelY) * pixelBrightness
		}
	}

	if totalWeight == 0 {
		return float64(peakX), float64(peakY), 0
	}
	return weightedX / totalWeight, weightedY / totalWeight, totalWeight
}

func grayBrightness(pixelColor colorLike) float64 {
	red16, green16, blue16, _ := pixelColor.RGBA()
	red := float64(red16 >> 8)
	green := float64(green16 >> 8)
	blue := float64(blue16 >> 8)
	return (0.2126 * red) + (0.7152 * green) + (0.0722 * blue)
}

type colorLike interface {
	RGBA() (r uint32, g uint32, b uint32, a uint32)
}

func buildCorrectionHint(centerOffsetX float64, centerOffsetY float64) string {
	const epsilon = 0.8
	if math.Abs(centerOffsetX) < epsilon && math.Abs(centerOffsetY) < epsilon {
		return "Target is centered. Keep current mount speed."
	}

	horizontalDirection := "left"
	if centerOffsetX < 0 {
		horizontalDirection = "right"
	}
	verticalDirection := "up"
	if centerOffsetY < 0 {
		verticalDirection = "down"
	}
	return "Move star image " + horizontalDirection + " and " + verticalDirection + " toward frame center."
}

func clampInt(rawValue int, minValue int, maxValue int) int {
	if rawValue < minValue {
		return minValue
	}
	if rawValue > maxValue {
		return maxValue
	}
	return rawValue
}

func clampFloat64(rawValue float64, minValue float64, maxValue float64) float64 {
	if rawValue < minValue {
		return minValue
	}
	if rawValue > maxValue {
		return maxValue
	}
	return rawValue
}
