package guiding

import (
	"errors"
	"image"
	"math"
	"sort"
)

const (
	defaultPhotoSearchStarLimit      = 6
	defaultCatalogMatchesPerStar     = 3
	minimumPhotoSearchStarLimit      = 3
	maximumPhotoSearchStarLimit      = 12
	minimumCatalogMatchesPerStar     = 1
	maximumCatalogMatchesPerStar     = 5
	minimumPhotoCandidateSeparation  = 8.0
	defaultMatchToleranceDegrees     = 4.0
	defaultCatalogNeighborRadiusDegs = 35.0
)

type CatalogMatch struct {
	Name            string  `json:"name"`
	Constellation   string  `json:"constellation"`
	VisualMagnitude float64 `json:"visual_magnitude"`
	MagnitudeDelta  float64 `json:"magnitude_delta"`
	AngularErrorDeg float64 `json:"angular_error_deg"`
}

type DetectedPhotoStar struct {
	X                float64        `json:"x"`
	Y                float64        `json:"y"`
	Brightness       float64        `json:"brightness"`
	DistanceToCenter float64        `json:"distance_to_center"`
	CatalogMatches   []CatalogMatch `json:"catalog_matches"`
}

type PhotoCatalogResult struct {
	CatalogProvider  string              `json:"catalog_provider"`
	FrameWidth       int                 `json:"frame_width"`
	FrameHeight      int                 `json:"frame_height"`
	DetectedCount    int                 `json:"detected_count"`
	CenterStar       DetectedPhotoStar   `json:"center_star"`
	SurroundingStars []DetectedPhotoStar `json:"surrounding_stars"`
}

type rawPhotoStar struct {
	x          int
	y          int
	brightness float64
}

type vector2 struct {
	x float64
	y float64
}

type catalogAlignment struct {
	centerCandidate    StarCatalogEntry
	surroundingMatches map[int]StarCatalogEntry
	errorByDetected    map[int]float64
	scale              float64
	rotationRadians    float64
	centerVector       vector2
}

// IdentifyStarsFromPhoto detects bright stars and solves the center-of-frame direction
// by matching relative geometry of several stars against the active catalog provider.
func IdentifyStarsFromPhoto(frame image.Image, maxStars int, maxCatalogMatches int) (PhotoCatalogResult, error) {
	bounds := frame.Bounds()
	frameWidth := bounds.Dx()
	frameHeight := bounds.Dy()
	if frameWidth < 3 || frameHeight < 3 {
		return PhotoCatalogResult{}, errors.New("frame is too small for star detection")
	}

	maxStars, maxCatalogMatches = resolvePhotoIdentifyLimits(maxStars, maxCatalogMatches)

	meanBrightness, standardDeviation := computeBrightnessStats(frame, bounds)
	minimumBrightness := meanBrightness + (1.2 * standardDeviation)
	if minimumBrightness < 24 {
		minimumBrightness = 24
	}

	rawCandidates := detectLocalPeakCandidates(frame, bounds, minimumBrightness)
	selectedCandidates := selectBrightCandidates(rawCandidates, maxStars, frameWidth, frameHeight)
	if len(selectedCandidates) == 0 {
		return PhotoCatalogResult{}, errors.New("no stars found in frame")
	}

	detectedStars := buildDetectedPhotoStars(selectedCandidates, frameWidth, frameHeight)
	alignment := solveCatalogAlignment(detectedStars)
	if alignment.centerCandidate.Name == "" {
		return PhotoCatalogResult{}, errors.New("failed to match star pattern with catalog")
	}

	primaryMatchesByDetectedIndex, predictedOffsetsByDetectedIndex := assignUniqueCatalogMatchesForDetectedStars(detectedStars, alignment)
	for detectedIndex := range detectedStars {
		primaryMatch, hasPrimaryMatch := primaryMatchesByDetectedIndex[detectedIndex]
		primaryError := alignment.errorByDetected[detectedIndex]
		predictedOffset := predictedOffsetsByDetectedIndex[detectedIndex]
		detectedStars[detectedIndex].CatalogMatches = buildCatalogMatchesForDetectedStar(detectedStars[detectedIndex], alignment.centerCandidate, primaryMatch, hasPrimaryMatch, primaryError, predictedOffset, maxCatalogMatches)
	}

	return PhotoCatalogResult{
		CatalogProvider:  ActiveCatalogProvider().ID,
		FrameWidth:       frameWidth,
		FrameHeight:      frameHeight,
		DetectedCount:    len(detectedStars),
		CenterStar:       detectedStars[0],
		SurroundingStars: detectedStars[1:],
	}, nil
}

func buildDetectedPhotoStars(selectedCandidates []rawPhotoStar, frameWidth int, frameHeight int) []DetectedPhotoStar {
	frameCenterX := float64(frameWidth-1) / 2
	frameCenterY := float64(frameHeight-1) / 2
	detectedStars := make([]DetectedPhotoStar, 0, len(selectedCandidates))
	for _, candidate := range selectedCandidates {
		distanceToCenter := math.Hypot(float64(candidate.x)-frameCenterX, float64(candidate.y)-frameCenterY)
		detectedStars = append(detectedStars, DetectedPhotoStar{
			X:                float64(candidate.x),
			Y:                float64(candidate.y),
			Brightness:       candidate.brightness,
			DistanceToCenter: distanceToCenter,
		})
	}

	sort.Slice(detectedStars, func(leftIndex int, rightIndex int) bool {
		return detectedStars[leftIndex].DistanceToCenter < detectedStars[rightIndex].DistanceToCenter
	})
	return detectedStars
}

func solveCatalogAlignment(detectedStars []DetectedPhotoStar) catalogAlignment {
	if len(detectedStars) < 3 {
		return catalogAlignment{}
	}

	catalogEntries := ActiveCatalogProvider().Entries
	if len(catalogEntries) < 3 {
		return catalogAlignment{}
	}

	bestScore := math.Inf(-1)
	bestAlignment := catalogAlignment{}
	centerDetected := detectedStars[0]
	centerVector := vector2{x: centerDetected.X, y: centerDetected.Y}

	for _, centerCandidate := range catalogEntries {
		neighborCandidates := collectCatalogNeighbors(centerCandidate, catalogEntries, defaultCatalogNeighborRadiusDegs)
		if len(neighborCandidates) < 2 {
			continue
		}

		for detectedReferenceIndex := 1; detectedReferenceIndex < len(detectedStars); detectedReferenceIndex += 1 {
			referenceDetectedVector := vectorFromPoints(centerVector, vector2{x: detectedStars[detectedReferenceIndex].X, y: detectedStars[detectedReferenceIndex].Y})
			referenceDetectedLength := vectorLength(referenceDetectedVector)
			if referenceDetectedLength < 0.0001 {
				continue
			}

			for _, referenceCatalog := range neighborCandidates {
				referenceCatalogVector := catalogOffsetVector(centerCandidate, referenceCatalog)
				referenceCatalogLength := vectorLength(referenceCatalogVector)
				if referenceCatalogLength < 0.0001 {
					continue
				}

				scale := referenceCatalogLength / referenceDetectedLength
				rotationRadians := vectorAngle(referenceCatalogVector) - vectorAngle(referenceDetectedVector)
				candidateScore, matchedCatalogByDetectedIndex, errorByDetectedIndex := scoreAlignmentHypothesis(centerCandidate, neighborCandidates, detectedStars, centerVector, scale, rotationRadians)
				if candidateScore > bestScore {
					bestScore = candidateScore
					bestAlignment = catalogAlignment{
						centerCandidate:    centerCandidate,
						surroundingMatches: matchedCatalogByDetectedIndex,
						errorByDetected:    errorByDetectedIndex,
						scale:              scale,
						rotationRadians:    rotationRadians,
						centerVector:       centerVector,
					}
				}
			}
		}
	}

	if bestScore < 12 {
		return catalogAlignment{}
	}
	return bestAlignment
}

func scoreAlignmentHypothesis(centerCandidate StarCatalogEntry, neighborCandidates []StarCatalogEntry, detectedStars []DetectedPhotoStar, centerVector vector2, scale float64, rotationRadians float64) (float64, map[int]StarCatalogEntry, map[int]float64) {
	matchedCatalogByDetectedIndex := make(map[int]StarCatalogEntry)
	errorByDetectedIndex := make(map[int]float64)
	usedCatalogNames := map[string]bool{centerCandidate.Name: true}
	totalError := 0.0
	matchedCount := 0.0

	for detectedIndex := 1; detectedIndex < len(detectedStars); detectedIndex += 1 {
		detectedOffsetVector := vectorFromPoints(centerVector, vector2{x: detectedStars[detectedIndex].X, y: detectedStars[detectedIndex].Y})
		predictedCatalogOffset := rotateVector(scaleVector(detectedOffsetVector, scale), rotationRadians)

		bestNeighborDistance := math.Inf(1)
		bestNeighbor := StarCatalogEntry{}
		for _, catalogNeighbor := range neighborCandidates {
			if usedCatalogNames[catalogNeighbor.Name] {
				continue
			}
			catalogNeighborOffset := catalogOffsetVector(centerCandidate, catalogNeighbor)
			distanceError := vectorLength(subtractVectors(catalogNeighborOffset, predictedCatalogOffset))
			if distanceError < bestNeighborDistance {
				bestNeighborDistance = distanceError
				bestNeighbor = catalogNeighbor
			}
		}

		if bestNeighbor.Name == "" || bestNeighborDistance > defaultMatchToleranceDegrees {
			continue
		}

		usedCatalogNames[bestNeighbor.Name] = true
		matchedCatalogByDetectedIndex[detectedIndex] = bestNeighbor
		errorByDetectedIndex[detectedIndex] = bestNeighborDistance
		totalError += bestNeighborDistance
		matchedCount += 1.0
	}

	if matchedCount == 0 {
		return math.Inf(-1), matchedCatalogByDetectedIndex, errorByDetectedIndex
	}
	averageError := totalError / matchedCount
	score := (matchedCount * 10.0) - (averageError * 1.5)
	errorByDetectedIndex[0] = averageError
	return score, matchedCatalogByDetectedIndex, errorByDetectedIndex
}

func buildCatalogMatchesForDetectedStar(detectedStar DetectedPhotoStar, centerCandidate StarCatalogEntry, primaryMatch StarCatalogEntry, hasPrimaryMatch bool, primaryError float64, predictedOffset vector2, maxMatches int) []CatalogMatch {
	maxMatches = clampInt(maxMatches, minimumCatalogMatchesPerStar, maximumCatalogMatchesPerStar)
	if maxMatches == 0 {
		maxMatches = defaultCatalogMatchesPerStar
	}

	matches := make([]CatalogMatch, 0, len(ActiveCatalogProvider().Entries))
	if hasPrimaryMatch {
		matches = append(matches, CatalogMatch{
			Name:            primaryMatch.Name,
			Constellation:   primaryMatch.Constellation,
			VisualMagnitude: primaryMatch.VisualMagnitude,
			MagnitudeDelta:  0,
			AngularErrorDeg: primaryError,
		})
	}

	estimatedMagnitude := estimateVisualMagnitude(detectedStar.Brightness)
	for _, catalogEntry := range ActiveCatalogProvider().Entries {
		if hasPrimaryMatch && catalogEntry.Name == primaryMatch.Name {
			continue
		}
		catalogEntryOffset := catalogOffsetVector(centerCandidate, catalogEntry)
		offsetError := vectorLength(subtractVectors(catalogEntryOffset, predictedOffset))
		matches = append(matches, CatalogMatch{
			Name:            catalogEntry.Name,
			Constellation:   catalogEntry.Constellation,
			VisualMagnitude: catalogEntry.VisualMagnitude,
			MagnitudeDelta:  math.Abs(catalogEntry.VisualMagnitude - estimatedMagnitude),
			AngularErrorDeg: offsetError,
		})
	}

	sort.Slice(matches, func(leftIndex int, rightIndex int) bool {
		leftScore := matches[leftIndex].AngularErrorDeg + (0.4 * matches[leftIndex].MagnitudeDelta)
		rightScore := matches[rightIndex].AngularErrorDeg + (0.4 * matches[rightIndex].MagnitudeDelta)
		return leftScore < rightScore
	})

	if maxMatches > len(matches) {
		maxMatches = len(matches)
	}
	return matches[:maxMatches]
}

func collectCatalogNeighbors(centerCandidate StarCatalogEntry, catalogEntries []StarCatalogEntry, maxRadiusDegrees float64) []StarCatalogEntry {
	neighbors := make([]StarCatalogEntry, 0, len(catalogEntries))
	for _, catalogEntry := range catalogEntries {
		if catalogEntry.Name == centerCandidate.Name {
			continue
		}
		offset := catalogOffsetVector(centerCandidate, catalogEntry)
		if vectorLength(offset) <= maxRadiusDegrees {
			neighbors = append(neighbors, catalogEntry)
		}
	}
	return neighbors
}

func assignUniqueCatalogMatchesForDetectedStars(detectedStars []DetectedPhotoStar, alignment catalogAlignment) (map[int]StarCatalogEntry, map[int]vector2) {
	primaryMatchesByDetectedIndex := make(map[int]StarCatalogEntry)
	predictedOffsetsByDetectedIndex := make(map[int]vector2)
	usedCatalogNames := map[string]bool{}
	primaryMatchesByDetectedIndex[0] = alignment.centerCandidate
	usedCatalogNames[alignment.centerCandidate.Name] = true
	predictedOffsetsByDetectedIndex[0] = vector2{x: 0, y: 0}

	detectedIndexes := make([]int, 0, len(detectedStars)-1)
	for detectedIndex := 1; detectedIndex < len(detectedStars); detectedIndex += 1 {
		detectedIndexes = append(detectedIndexes, detectedIndex)
	}
	sort.Slice(detectedIndexes, func(left int, right int) bool {
		return detectedStars[detectedIndexes[left]].DistanceToCenter > detectedStars[detectedIndexes[right]].DistanceToCenter
	})

	catalogEntries := ActiveCatalogProvider().Entries
	for _, detectedIndex := range detectedIndexes {
		detectedOffsetVector := vectorFromPoints(alignment.centerVector, vector2{x: detectedStars[detectedIndex].X, y: detectedStars[detectedIndex].Y})
		predictedOffset := rotateVector(scaleVector(detectedOffsetVector, alignment.scale), alignment.rotationRadians)
		predictedOffsetsByDetectedIndex[detectedIndex] = predictedOffset
		estimatedMagnitude := estimateVisualMagnitude(detectedStars[detectedIndex].Brightness)

		bestCatalogEntry := StarCatalogEntry{}
		bestCatalogScore := math.Inf(1)
		for _, catalogEntry := range catalogEntries {
			if usedCatalogNames[catalogEntry.Name] {
				continue
			}
			catalogOffset := catalogOffsetVector(alignment.centerCandidate, catalogEntry)
			offsetError := vectorLength(subtractVectors(catalogOffset, predictedOffset))
			magnitudeError := math.Abs(catalogEntry.VisualMagnitude - estimatedMagnitude)
			catalogScore := offsetError + (0.35 * magnitudeError)
			if catalogScore < bestCatalogScore {
				bestCatalogScore = catalogScore
				bestCatalogEntry = catalogEntry
			}
		}

		if bestCatalogEntry.Name == "" {
			continue
		}
		primaryMatchesByDetectedIndex[detectedIndex] = bestCatalogEntry
		usedCatalogNames[bestCatalogEntry.Name] = true
		alignment.errorByDetected[detectedIndex] = bestCatalogScore
	}

	return primaryMatchesByDetectedIndex, predictedOffsetsByDetectedIndex
}

func catalogOffsetVector(centerCandidate StarCatalogEntry, otherEntry StarCatalogEntry) vector2 {
	raDifferenceDegrees := raDifferenceHoursSigned(centerCandidate.RightAscensionHour, otherEntry.RightAscensionHour) * 15.0
	declinationAverageRadians := ((centerCandidate.DeclinationDeg + otherEntry.DeclinationDeg) / 2.0) * (math.Pi / 180.0)
	xOffset := raDifferenceDegrees * math.Cos(declinationAverageRadians)
	yOffset := otherEntry.DeclinationDeg - centerCandidate.DeclinationDeg
	return vector2{x: xOffset, y: yOffset}
}

func raDifferenceHoursSigned(leftRA float64, rightRA float64) float64 {
	difference := rightRA - leftRA
	for difference > 12 {
		difference -= 24
	}
	for difference < -12 {
		difference += 24
	}
	return difference
}

func computeBrightnessStats(frame image.Image, bounds image.Rectangle) (float64, float64) {
	pixelCount := float64(bounds.Dx() * bounds.Dy())
	if pixelCount == 0 {
		return 0, 0
	}

	sum := 0.0
	sumSquares := 0.0
	for pixelY := 0; pixelY < bounds.Dy(); pixelY += 1 {
		for pixelX := 0; pixelX < bounds.Dx(); pixelX += 1 {
			brightness := grayBrightness(frame.At(pixelX+bounds.Min.X, pixelY+bounds.Min.Y))
			sum += brightness
			sumSquares += brightness * brightness
		}
	}
	mean := sum / pixelCount
	variance := (sumSquares / pixelCount) - (mean * mean)
	if variance < 0 {
		variance = 0
	}
	return mean, math.Sqrt(variance)
}

func detectLocalPeakCandidates(frame image.Image, bounds image.Rectangle, minimumBrightness float64) []rawPhotoStar {
	candidates := make([]rawPhotoStar, 0, 128)
	for pixelY := 1; pixelY < bounds.Dy()-1; pixelY += 1 {
		for pixelX := 1; pixelX < bounds.Dx()-1; pixelX += 1 {
			centerBrightness := grayBrightness(frame.At(pixelX+bounds.Min.X, pixelY+bounds.Min.Y))
			if centerBrightness < minimumBrightness {
				continue
			}
			if !isLocalBrightnessPeak(frame, bounds, pixelX, pixelY, centerBrightness) {
				continue
			}
			candidates = append(candidates, rawPhotoStar{x: pixelX, y: pixelY, brightness: centerBrightness})
		}
	}
	return candidates
}

func isLocalBrightnessPeak(frame image.Image, bounds image.Rectangle, centerX int, centerY int, centerBrightness float64) bool {
	for offsetY := -1; offsetY <= 1; offsetY += 1 {
		for offsetX := -1; offsetX <= 1; offsetX += 1 {
			if offsetX == 0 && offsetY == 0 {
				continue
			}
			neighborBrightness := grayBrightness(frame.At(centerX+offsetX+bounds.Min.X, centerY+offsetY+bounds.Min.Y))
			if neighborBrightness > centerBrightness {
				return false
			}
		}
	}
	return true
}

func selectBrightCandidates(candidates []rawPhotoStar, maxStars int, frameWidth int, frameHeight int) []rawPhotoStar {
	sort.Slice(candidates, func(leftIndex int, rightIndex int) bool {
		return candidates[leftIndex].brightness > candidates[rightIndex].brightness
	})

	minimumSeparation := minimumPhotoCandidateSeparation
	if shorterSide := math.Min(float64(frameWidth), float64(frameHeight)); shorterSide > 0 {
		dynamicSeparation := shorterSide / 28
		if dynamicSeparation > minimumSeparation {
			minimumSeparation = dynamicSeparation
		}
	}

	if len(candidates) == 0 {
		return nil
	}
	candidateWindowLimit := clampInt(maxStars*30, maxStars, len(candidates))
	limitedCandidates := candidates[:candidateWindowLimit]
	selected := make([]rawPhotoStar, 0, maxStars)
	selected = append(selected, limitedCandidates[0])
	for len(selected) < maxStars {
		bestCandidateIndex := -1
		bestCandidateDistanceScore := -1.0
		for candidateIndex, candidate := range limitedCandidates {
			if containsRawPhotoStar(selected, candidate) {
				continue
			}
			minimumDistanceToSelected := math.Inf(1)
			for _, acceptedCandidate := range selected {
				distance := math.Hypot(float64(candidate.x-acceptedCandidate.x), float64(candidate.y-acceptedCandidate.y))
				if distance < minimumDistanceToSelected {
					minimumDistanceToSelected = distance
				}
			}
			if minimumDistanceToSelected < minimumSeparation {
				continue
			}
			if minimumDistanceToSelected > bestCandidateDistanceScore {
				bestCandidateDistanceScore = minimumDistanceToSelected
				bestCandidateIndex = candidateIndex
			}
		}
		if bestCandidateIndex < 0 {
			break
		}
		selected = append(selected, limitedCandidates[bestCandidateIndex])
	}

	return selected
}

func containsRawPhotoStar(selectedCandidates []rawPhotoStar, candidate rawPhotoStar) bool {
	for _, selectedCandidate := range selectedCandidates {
		if selectedCandidate.x == candidate.x && selectedCandidate.y == candidate.y {
			return true
		}
	}
	return false
}

func estimateVisualMagnitude(starBrightness float64) float64 {
	clampedBrightness := clampFloat64(starBrightness, 0, 255)
	brightnessRatio := clampedBrightness / 255
	return 3.5 - (brightnessRatio * 5.0)
}

func resolvePhotoIdentifyLimits(rawMaxStars int, rawMaxCatalogMatches int) (int, int) {
	resolvedMaxStars := rawMaxStars
	if resolvedMaxStars == 0 {
		resolvedMaxStars = defaultPhotoSearchStarLimit
	}
	resolvedMaxStars = clampInt(resolvedMaxStars, minimumPhotoSearchStarLimit, maximumPhotoSearchStarLimit)

	resolvedMaxCatalogMatches := rawMaxCatalogMatches
	if resolvedMaxCatalogMatches == 0 {
		resolvedMaxCatalogMatches = defaultCatalogMatchesPerStar
	}
	resolvedMaxCatalogMatches = clampInt(resolvedMaxCatalogMatches, minimumCatalogMatchesPerStar, maximumCatalogMatchesPerStar)

	return resolvedMaxStars, resolvedMaxCatalogMatches
}

func vectorFromPoints(start vector2, end vector2) vector2 {
	return vector2{x: end.x - start.x, y: end.y - start.y}
}

func vectorLength(source vector2) float64 {
	return math.Hypot(source.x, source.y)
}

func vectorAngle(source vector2) float64 {
	return math.Atan2(source.y, source.x)
}

func scaleVector(source vector2, factor float64) vector2 {
	return vector2{x: source.x * factor, y: source.y * factor}
}

func rotateVector(source vector2, rotationRadians float64) vector2 {
	cosAngle := math.Cos(rotationRadians)
	sinAngle := math.Sin(rotationRadians)
	return vector2{
		x: (source.x * cosAngle) - (source.y * sinAngle),
		y: (source.x * sinAngle) + (source.y * cosAngle),
	}
}

func subtractVectors(left vector2, right vector2) vector2 {
	return vector2{x: left.x - right.x, y: left.y - right.y}
}
