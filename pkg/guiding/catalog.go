package guiding

import (
	"errors"
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

type CatalogProvider struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Available   bool               `json:"available"`
	Entries     []StarCatalogEntry `json:"-"`
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
	{Name: "Achernar", Constellation: "Eridanus", RightAscensionHour: 1.628, DeclinationDeg: -57.237, VisualMagnitude: 0.46},
	{Name: "Hadar", Constellation: "Centaurus", RightAscensionHour: 14.063, DeclinationDeg: -60.373, VisualMagnitude: 0.61},
	{Name: "Altair", Constellation: "Aquila", RightAscensionHour: 19.846, DeclinationDeg: 8.868, VisualMagnitude: 0.77},
	{Name: "Acrux", Constellation: "Crux", RightAscensionHour: 12.443, DeclinationDeg: -63.099, VisualMagnitude: 0.76},
	{Name: "Aldebaran", Constellation: "Taurus", RightAscensionHour: 4.598, DeclinationDeg: 16.509, VisualMagnitude: 0.85},
	{Name: "Spica", Constellation: "Virgo", RightAscensionHour: 13.419, DeclinationDeg: -11.161, VisualMagnitude: 0.97},
	{Name: "Antares", Constellation: "Scorpius", RightAscensionHour: 16.49, DeclinationDeg: -26.432, VisualMagnitude: 1.06},
	{Name: "Pollux", Constellation: "Gemini", RightAscensionHour: 7.756, DeclinationDeg: 28.026, VisualMagnitude: 1.14},
	{Name: "Fomalhaut", Constellation: "Piscis Austrinus", RightAscensionHour: 22.961, DeclinationDeg: -29.622, VisualMagnitude: 1.16},
	{Name: "Deneb", Constellation: "Cygnus", RightAscensionHour: 20.691, DeclinationDeg: 45.281, VisualMagnitude: 1.25},
	{Name: "Regulus", Constellation: "Leo", RightAscensionHour: 10.139, DeclinationDeg: 11.967, VisualMagnitude: 1.35},
	{Name: "Adhara", Constellation: "Canis Major", RightAscensionHour: 6.977, DeclinationDeg: -28.972, VisualMagnitude: 1.50},
	{Name: "Shaula", Constellation: "Scorpius", RightAscensionHour: 17.561, DeclinationDeg: -37.104, VisualMagnitude: 1.62},
	{Name: "Bellatrix", Constellation: "Orion", RightAscensionHour: 5.418, DeclinationDeg: 6.349, VisualMagnitude: 1.64},
	{Name: "Elnath", Constellation: "Taurus", RightAscensionHour: 5.438, DeclinationDeg: 28.607, VisualMagnitude: 1.65},
	{Name: "Miaplacidus", Constellation: "Carina", RightAscensionHour: 9.22, DeclinationDeg: -69.717, VisualMagnitude: 1.67},
	{Name: "Alnilam", Constellation: "Orion", RightAscensionHour: 5.603, DeclinationDeg: -1.201, VisualMagnitude: 1.69},
	{Name: "Alnair", Constellation: "Grus", RightAscensionHour: 22.137, DeclinationDeg: -46.961, VisualMagnitude: 1.74},
	{Name: "Alioth", Constellation: "Ursa Major", RightAscensionHour: 12.901, DeclinationDeg: 55.959, VisualMagnitude: 1.77},
	{Name: "Dubhe", Constellation: "Ursa Major", RightAscensionHour: 11.062, DeclinationDeg: 61.751, VisualMagnitude: 1.79},
	{Name: "Mirfak", Constellation: "Perseus", RightAscensionHour: 3.405, DeclinationDeg: 49.861, VisualMagnitude: 1.79},
	{Name: "Wezen", Constellation: "Canis Major", RightAscensionHour: 7.139, DeclinationDeg: -26.393, VisualMagnitude: 1.83},
}

var catalogProviders = []CatalogProvider{
	{
		ID:          "yale_bsc5_embedded",
		Title:       "Yale Bright Star Catalog (embedded bright subset)",
		Description: "Primary offline provider for on-device plate solving.",
		Entries:     brightStarCatalog,
	},
	{
		ID:          "vizier_gaia_dr3_online",
		Title:       "VizieR Gaia DR3 (online)",
		Description: "Future high-density provider for deep-field solving.",
	},
	{
		ID:          "simbad_online",
		Title:       "SIMBAD (online)",
		Description: "Future object metadata and cross-identification provider.",
	},
}

func ActiveCatalogProvider() CatalogProvider {
	for _, catalogProvider := range catalogProviders {
		if catalogProvider.ID == activeCatalogProviderID {
			return catalogProvider
		}
	}
	return catalogProviders[0]
}

var activeCatalogProviderID = catalogProviders[0].ID

func ListCatalogProviders() []CatalogProvider {
	visibleProviders := make([]CatalogProvider, 0, len(catalogProviders))
	for _, provider := range catalogProviders {
		visibleProviders = append(visibleProviders, CatalogProvider{
			ID:          provider.ID,
			Title:       provider.Title,
			Description: provider.Description,
			Available:   len(provider.Entries) > 0,
		})
	}
	return visibleProviders
}

func SetActiveCatalogProvider(providerID string) error {
	normalizedProviderID := strings.TrimSpace(providerID)
	if normalizedProviderID == "" {
		return errors.New("provider id is required")
	}
	for _, catalogProvider := range catalogProviders {
		if catalogProvider.ID == normalizedProviderID {
			if len(catalogProvider.Entries) == 0 {
				return errors.New("provider is not installed locally")
			}
			activeCatalogProviderID = normalizedProviderID
			return nil
		}
	}
	return errors.New("unknown provider id")
}

func FindStarByName(query string) []StarCatalogEntry {
	normalizedQuery := strings.ToLower(strings.TrimSpace(query))
	if normalizedQuery == "" {
		return nil
	}

	catalogEntries := ActiveCatalogProvider().Entries
	matchedEntries := make([]StarCatalogEntry, 0, len(catalogEntries))
	for _, starEntry := range catalogEntries {
		normalizedName := strings.ToLower(starEntry.Name)
		if strings.Contains(normalizedName, normalizedQuery) {
			matchedEntries = append(matchedEntries, starEntry)
		}
	}
	return matchedEntries
}

func FindNearestStar(rightAscensionHour float64, declinationDeg float64) StarCatalogEntry {
	catalogEntries := ActiveCatalogProvider().Entries
	nearestEntry := catalogEntries[0]
	nearestDistance := math.MaxFloat64
	for _, starEntry := range catalogEntries {
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
