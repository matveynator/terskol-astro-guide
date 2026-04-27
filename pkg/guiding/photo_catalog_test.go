package guiding

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestIdentifyStarsFromPhotoSolvesCenterAndNeighborsByGeometry(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 240, 180))
	fillFrame(frame, color.RGBA{R: 3, G: 3, B: 3, A: 255})

	catalogCenter := mustFindCatalogStarByNameForTest(t, "Betelgeuse")
	catalogNeighborA := mustFindCatalogStarByNameForTest(t, "Bellatrix")
	catalogNeighborB := mustFindCatalogStarByNameForTest(t, "Alnilam")
	catalogNeighborC := mustFindCatalogStarByNameForTest(t, "Rigel")

	frameCenterX := 120.0
	frameCenterY := 90.0
	placeStar(frame, int(frameCenterX), int(frameCenterY), 255)
	placeCatalogProjectedStar(frame, catalogCenter, catalogNeighborA, frameCenterX, frameCenterY, 8.0, math.Pi/7, 230)
	placeCatalogProjectedStar(frame, catalogCenter, catalogNeighborB, frameCenterX, frameCenterY, 8.0, math.Pi/7, 225)
	placeCatalogProjectedStar(frame, catalogCenter, catalogNeighborC, frameCenterX, frameCenterY, 8.0, math.Pi/7, 220)

	result, err := IdentifyStarsFromPhoto(frame, 6, 3)
	if err != nil {
		t.Fatalf("expected no identify error, got %v", err)
	}
	if result.CenterStar.CatalogMatches[0].Name != "Betelgeuse" {
		t.Fatalf("expected center star Betelgeuse, got %q", result.CenterStar.CatalogMatches[0].Name)
	}
	if len(result.SurroundingStars) < 2 {
		t.Fatalf("expected at least 2 surrounding stars, got %d", len(result.SurroundingStars))
	}
}

func TestIdentifyStarsFromPhotoReturnsErrorWhenNoCandidatesFound(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 60, 40))
	fillFrame(frame, color.RGBA{R: 0, G: 0, B: 0, A: 255})

	_, err := IdentifyStarsFromPhoto(frame, 4, 2)
	if err == nil {
		t.Fatalf("expected error when no stars are visible")
	}
}

func TestResolvePhotoIdentifyLimitsAppliesDefaultsBeforeClamp(t *testing.T) {
	resolvedMaxStars, resolvedMaxCatalogMatches := resolvePhotoIdentifyLimits(0, 0)
	if resolvedMaxStars != defaultPhotoSearchStarLimit {
		t.Fatalf("expected default max stars %d, got %d", defaultPhotoSearchStarLimit, resolvedMaxStars)
	}
	if resolvedMaxCatalogMatches != defaultCatalogMatchesPerStar {
		t.Fatalf("expected default max catalog matches %d, got %d", defaultCatalogMatchesPerStar, resolvedMaxCatalogMatches)
	}
}

func TestResolvePhotoIdentifyLimitsClampsExplicitValues(t *testing.T) {
	resolvedMaxStars, resolvedMaxCatalogMatches := resolvePhotoIdentifyLimits(99, -7)
	if resolvedMaxStars != maximumPhotoSearchStarLimit {
		t.Fatalf("expected clamped max stars %d, got %d", maximumPhotoSearchStarLimit, resolvedMaxStars)
	}
	if resolvedMaxCatalogMatches != minimumCatalogMatchesPerStar {
		t.Fatalf("expected clamped max catalog matches %d, got %d", minimumCatalogMatchesPerStar, resolvedMaxCatalogMatches)
	}
}

func mustFindCatalogStarByNameForTest(t *testing.T, targetName string) StarCatalogEntry {
	t.Helper()
	for _, catalogEntry := range ActiveCatalogProvider().Entries {
		if catalogEntry.Name == targetName {
			return catalogEntry
		}
	}
	t.Fatalf("catalog star %q not found", targetName)
	return StarCatalogEntry{}
}

func placeCatalogProjectedStar(frame *image.RGBA, catalogCenter StarCatalogEntry, catalogNeighbor StarCatalogEntry, frameCenterX float64, frameCenterY float64, pixelScale float64, rotationRadians float64, brightness uint8) {
	catalogOffset := catalogOffsetVector(catalogCenter, catalogNeighbor)
	projectedOffset := rotateVector(scaleVector(catalogOffset, pixelScale), rotationRadians)
	projectedX := int(math.Round(frameCenterX + projectedOffset.x))
	projectedY := int(math.Round(frameCenterY + projectedOffset.y))
	placeStar(frame, projectedX, projectedY, brightness)
}

func fillFrame(frame *image.RGBA, fillColor color.RGBA) {
	for y := 0; y < frame.Bounds().Dy(); y += 1 {
		for x := 0; x < frame.Bounds().Dx(); x += 1 {
			frame.SetRGBA(x, y, fillColor)
		}
	}
}

func placeStar(frame *image.RGBA, centerX int, centerY int, peak uint8) {
	for offsetY := -1; offsetY <= 1; offsetY += 1 {
		for offsetX := -1; offsetX <= 1; offsetX += 1 {
			x := centerX + offsetX
			y := centerY + offsetY
			if x < 0 || y < 0 || x >= frame.Bounds().Dx() || y >= frame.Bounds().Dy() {
				continue
			}
			weight := uint8(0)
			if offsetX == 0 && offsetY == 0 {
				weight = peak
			} else {
				weight = peak / 2
			}
			frame.SetRGBA(x, y, color.RGBA{R: weight, G: weight, B: weight, A: 255})
		}
	}
}
