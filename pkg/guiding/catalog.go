package guiding

import (
	"math"
	"strings"
)

type StarCatalogEntry struct {
	Name               string  `json:"name"`
	Constellation      string  `json:"constellation"`
	RightAscensionHour float64 `json:"right_ascension_hour"`
	DeclinationDeg     float64 `json:"declination_deg"`
	VisualMagnitude    float64 `json:"visual_magnitude"`
}

var brightStarCatalog = []StarCatalogEntry{
	{Name: "Sirius", Constellation: "Canis Major", RightAscensionHour: 6.752, DeclinationDeg: -16.716, VisualMagnitude: -1.46},
	{Name: "Canopus", Constellation: "Carina", RightAscensionHour: 6.399, DeclinationDeg: -52.695, VisualMagnitude: -0.74},
	{Name: "Arcturus", Constellation: "Bootes", RightAscensionHour: 14.261, DeclinationDeg: 19.182, VisualMagnitude: -0.05},
	{Name: "Vega", Constellation: "Lyra", RightAscensionHour: 18.615, DeclinationDeg: 38.783, VisualMagnitude: 0.03},
	{Name: "Capella", Constellation: "Auriga", RightAscensionHour: 5.279, DeclinationDeg: 45.997, VisualMagnitude: 0.08},
	{Name: "Rigel", Constellation: "Orion", RightAscensionHour: 5.243, DeclinationDeg: -8.202, VisualMagnitude: 0.13},
	{Name: "Procyon", Constellation: "Canis Minor", RightAscensionHour: 7.655, DeclinationDeg: 5.225, VisualMagnitude: 0.38},
	{Name: "Betelgeuse", Constellation: "Orion", RightAscensionHour: 5.919, DeclinationDeg: 7.407, VisualMagnitude: 0.50},
	{Name: "Aldebaran", Constellation: "Taurus", RightAscensionHour: 4.598, DeclinationDeg: 16.509, VisualMagnitude: 0.85},
	{Name: "Deneb", Constellation: "Cygnus", RightAscensionHour: 20.691, DeclinationDeg: 45.281, VisualMagnitude: 1.25},
}

func FindStarByName(query string) []StarCatalogEntry {
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return nil
	}

	matchedEntries := make([]StarCatalogEntry, 0, len(brightStarCatalog))
	for _, starEntry := range brightStarCatalog {
		normalizedName := strings.ToLower(starEntry.Name)
		if strings.Contains(normalizedName, normalizedQuery) {
			matchedEntries = append(matchedEntries, starEntry)
		}
	}
	return matchedEntries
}

func FindNearestStar(rightAscensionHour float64, declinationDeg float64) StarCatalogEntry {
	nearestEntry := brightStarCatalog[0]
	nearestDistance := math.MaxFloat64
	for _, starEntry := range brightStarCatalog {
		rightAscensionDistance := math.Abs(starEntry.RightAscensionHour - rightAscensionHour)
		declinationDistance := math.Abs(starEntry.DeclinationDeg - declinationDeg)
		combinedDistance := (rightAscensionDistance * rightAscensionDistance) + (declinationDistance * declinationDistance)
		if combinedDistance < nearestDistance {
			nearestDistance = combinedDistance
			nearestEntry = starEntry
		}
	}
	return nearestEntry
}
