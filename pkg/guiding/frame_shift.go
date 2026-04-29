package guiding

import (
	"errors"
	"image"
	"math"
	"sort"
)

const (
	defaultGuideStarLimit           = 40
	minimumGuideStarLimit           = 5
	maximumGuideStarLimit           = 128
	minimumShiftVoteSupport         = 3
	shiftVoteQuantizationPixels     = 1.0
	defaultMatchTolerancePixels     = 2.6
	minimumMatchCountForRotationFit = 2
)

type FrameShiftRequest struct {
	ReferenceFrame image.Image
	CurrentFrame   image.Image
	MaxStars       int
	PixelToMotor   PixelToMotorMatrix
}

type PixelToMotorMatrix struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
	C float64 `json:"c"`
	D float64 `json:"d"`
}

type FrameShiftResult struct {
	ReferenceStars int                `json:"reference_stars"`
	CurrentStars   int                `json:"current_stars"`
	MatchedStars   int                `json:"matched_stars"`
	DeltaX         float64            `json:"delta_x"`
	DeltaY         float64            `json:"delta_y"`
	RotationDeg    float64            `json:"rotation_deg"`
	Confidence     float64            `json:"confidence"`
	ResidualRMS    float64            `json:"residual_rms"`
	SuggestedMotor SuggestedMotorHint `json:"suggested_motor"`
}

type SuggestedMotorHint struct {
	MotorXMs float64 `json:"motor_x_ms"`
	MotorYMs float64 `json:"motor_y_ms"`
}

type guideStar struct {
	x          float64
	y          float64
	brightness float64
}

type shiftVote struct {
	dxBin float64
	dyBin float64
	count int
}

type matchedStarPair struct {
	reference guideStar
	current   guideStar
}

// AnalyzeFrameShift estimates field drift between two frames using multiple stars.
// The solver is intentionally deterministic and conservative to make field debugging easier.
func AnalyzeFrameShift(request FrameShiftRequest) (FrameShiftResult, error) {
	if request.ReferenceFrame == nil || request.CurrentFrame == nil {
		return FrameShiftResult{}, errors.New("reference and current frames are required")
	}

	referenceBounds := request.ReferenceFrame.Bounds()
	currentBounds := request.CurrentFrame.Bounds()
	if referenceBounds.Dx() != currentBounds.Dx() || referenceBounds.Dy() != currentBounds.Dy() {
		return FrameShiftResult{}, errors.New("reference and current frame sizes must match")
	}

	maxStars := clampInt(request.MaxStars, minimumGuideStarLimit, maximumGuideStarLimit)
	if maxStars == 0 {
		maxStars = defaultGuideStarLimit
	}

	referenceStars := detectGuideStars(request.ReferenceFrame, maxStars)
	currentStars := detectGuideStars(request.CurrentFrame, maxStars)
	if len(referenceStars) < minimumShiftVoteSupport || len(currentStars) < minimumShiftVoteSupport {
		return FrameShiftResult{}, errors.New("not enough stars for robust shift measurement")
	}

	coarseShift, voteSupport := estimateCoarseShift(referenceStars, currentStars)
	if voteSupport < minimumShiftVoteSupport {
		return FrameShiftResult{}, errors.New("failed to estimate coarse shift")
	}

	matchedPairs := matchStarsByShift(referenceStars, currentStars, coarseShift)
	if len(matchedPairs) < minimumShiftVoteSupport {
		return FrameShiftResult{}, errors.New("not enough matched stars for shift estimation")
	}

	deltaX, deltaY := fitTranslation(matchedPairs)
	rotationRadians := fitRotationRadians(matchedPairs, deltaX, deltaY)
	residualRMS := computeResidualRMS(matchedPairs, deltaX, deltaY, rotationRadians)
	confidence := computeShiftConfidence(len(referenceStars), len(currentStars), len(matchedPairs), residualRMS)

	suggestedMotor := SuggestedMotorHint{}
	if request.PixelToMotor != (PixelToMotorMatrix{}) {
		suggestedMotor = SuggestedMotorHint{
			MotorXMs: -(request.PixelToMotor.A*deltaX + request.PixelToMotor.B*deltaY),
			MotorYMs: -(request.PixelToMotor.C*deltaX + request.PixelToMotor.D*deltaY),
		}
	}

	return FrameShiftResult{
		ReferenceStars: len(referenceStars),
		CurrentStars:   len(currentStars),
		MatchedStars:   len(matchedPairs),
		DeltaX:         deltaX,
		DeltaY:         deltaY,
		RotationDeg:    rotationRadians * 180 / math.Pi,
		Confidence:     confidence,
		ResidualRMS:    residualRMS,
		SuggestedMotor: suggestedMotor,
	}, nil
}

func detectGuideStars(frame image.Image, maxStars int) []guideStar {
	bounds := frame.Bounds()
	meanBrightness, standardDeviation := computeBrightnessStats(frame, bounds)
	minimumBrightness := meanBrightness + (1.4 * standardDeviation)
	if minimumBrightness < 20 {
		minimumBrightness = 20
	}

	rawCandidates := detectLocalPeakCandidates(frame, bounds, minimumBrightness)
	sort.Slice(rawCandidates, func(leftIndex int, rightIndex int) bool {
		return rawCandidates[leftIndex].brightness > rawCandidates[rightIndex].brightness
	})

	stars := make([]guideStar, 0, maxStars)
	for _, candidate := range rawCandidates {
		if len(stars) >= maxStars {
			break
		}
		if !isCandidateSeparated(stars, float64(candidate.x), float64(candidate.y), minimumPhotoCandidateSeparation) {
			continue
		}
		centroidX, centroidY, weight := computeLocalCentroid(frame, bounds, candidate.x, candidate.y)
		if weight <= 0 {
			continue
		}
		stars = append(stars, guideStar{x: centroidX, y: centroidY, brightness: candidate.brightness})
	}
	return stars
}

func estimateCoarseShift(referenceStars []guideStar, currentStars []guideStar) (vector2, int) {
	shiftCounts := make(map[[2]int]int)
	for _, referenceStar := range referenceStars {
		for _, currentStar := range currentStars {
			dx := currentStar.x - referenceStar.x
			dy := currentStar.y - referenceStar.y
			quantizedX := int(math.Round(dx / shiftVoteQuantizationPixels))
			quantizedY := int(math.Round(dy / shiftVoteQuantizationPixels))
			shiftCounts[[2]int{quantizedX, quantizedY}] += 1
		}
	}

	bestVote := shiftVote{count: -1}
	for key, count := range shiftCounts {
		if count > bestVote.count {
			bestVote = shiftVote{dxBin: float64(key[0]), dyBin: float64(key[1]), count: count}
		}
	}

	return vector2{x: bestVote.dxBin * shiftVoteQuantizationPixels, y: bestVote.dyBin * shiftVoteQuantizationPixels}, bestVote.count
}

func matchStarsByShift(referenceStars []guideStar, currentStars []guideStar, coarseShift vector2) []matchedStarPair {
	pairs := make([]matchedStarPair, 0, len(referenceStars))
	usedCurrentIndexes := make(map[int]bool)

	for _, referenceStar := range referenceStars {
		predictedX := referenceStar.x + coarseShift.x
		predictedY := referenceStar.y + coarseShift.y
		bestIndex := -1
		bestDistance := math.Inf(1)
		for currentIndex, currentStar := range currentStars {
			if usedCurrentIndexes[currentIndex] {
				continue
			}
			distance := math.Hypot(currentStar.x-predictedX, currentStar.y-predictedY)
			if distance < bestDistance {
				bestDistance = distance
				bestIndex = currentIndex
			}
		}
		if bestIndex < 0 || bestDistance > defaultMatchTolerancePixels {
			continue
		}

		usedCurrentIndexes[bestIndex] = true
		pairs = append(pairs, matchedStarPair{reference: referenceStar, current: currentStars[bestIndex]})
	}
	return pairs
}

func fitTranslation(pairs []matchedStarPair) (float64, float64) {
	sumX := 0.0
	sumY := 0.0
	sumWeight := 0.0
	for _, pair := range pairs {
		weight := math.Max(pair.reference.brightness, 1)
		sumX += (pair.current.x - pair.reference.x) * weight
		sumY += (pair.current.y - pair.reference.y) * weight
		sumWeight += weight
	}
	if sumWeight == 0 {
		return 0, 0
	}
	return sumX / sumWeight, sumY / sumWeight
}

func fitRotationRadians(pairs []matchedStarPair, deltaX float64, deltaY float64) float64 {
	if len(pairs) < minimumMatchCountForRotationFit {
		return 0
	}

	referenceCenterX := 0.0
	referenceCenterY := 0.0
	currentCenterX := 0.0
	currentCenterY := 0.0

	for _, pair := range pairs {
		referenceCenterX += pair.reference.x
		referenceCenterY += pair.reference.y
		currentCenterX += pair.current.x - deltaX
		currentCenterY += pair.current.y - deltaY
	}

	count := float64(len(pairs))
	referenceCenterX /= count
	referenceCenterY /= count
	currentCenterX /= count
	currentCenterY /= count

	numerator := 0.0
	denominator := 0.0
	for _, pair := range pairs {
		referenceX := pair.reference.x - referenceCenterX
		referenceY := pair.reference.y - referenceCenterY
		currentX := (pair.current.x - deltaX) - currentCenterX
		currentY := (pair.current.y - deltaY) - currentCenterY
		numerator += (referenceX * currentY) - (referenceY * currentX)
		denominator += (referenceX * currentX) + (referenceY * currentY)
	}
	if numerator == 0 && denominator == 0 {
		return 0
	}
	return math.Atan2(numerator, denominator)
}

func computeResidualRMS(pairs []matchedStarPair, deltaX float64, deltaY float64, rotationRadians float64) float64 {
	if len(pairs) == 0 {
		return 0
	}
	meanReferenceX := 0.0
	meanReferenceY := 0.0
	for _, pair := range pairs {
		meanReferenceX += pair.reference.x
		meanReferenceY += pair.reference.y
	}
	meanReferenceX /= float64(len(pairs))
	meanReferenceY /= float64(len(pairs))

	cosTheta := math.Cos(rotationRadians)
	sinTheta := math.Sin(rotationRadians)
	sumSquared := 0.0
	for _, pair := range pairs {
		referenceX := pair.reference.x - meanReferenceX
		referenceY := pair.reference.y - meanReferenceY
		rotatedX := (referenceX * cosTheta) - (referenceY * sinTheta)
		rotatedY := (referenceX * sinTheta) + (referenceY * cosTheta)
		predictedX := rotatedX + meanReferenceX + deltaX
		predictedY := rotatedY + meanReferenceY + deltaY
		residualX := pair.current.x - predictedX
		residualY := pair.current.y - predictedY
		sumSquared += (residualX * residualX) + (residualY * residualY)
	}
	return math.Sqrt(sumSquared / float64(len(pairs)))
}

func computeShiftConfidence(referenceCount int, currentCount int, matchedCount int, residualRMS float64) float64 {
	if referenceCount == 0 || currentCount == 0 {
		return 0
	}
	coverage := float64(matchedCount) / math.Min(float64(referenceCount), float64(currentCount))
	residualPenalty := math.Exp(-0.7 * residualRMS)
	return clampFloat64(coverage*residualPenalty, 0, 1)
}

func isCandidateSeparated(stars []guideStar, candidateX float64, candidateY float64, minDistance float64) bool {
	for _, star := range stars {
		if math.Hypot(star.x-candidateX, star.y-candidateY) < minDistance {
			return false
		}
	}
	return true
}
