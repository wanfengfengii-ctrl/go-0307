package tests

import (
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// newStore opens a fresh in-memory store for a test and registers cleanup.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fixedPlan returns the canonical test plan: a chamber, shelf, condenser and
// drain region with four probe positions across two load layers.
func fixedPlan() domain.ValidationPlan {
	regions := []domain.ChamberRegion{
		{ID: "r-chamber", Name: "腔体", Kind: domain.RegionChamber},
		{ID: "r-shelf", Name: "搁板", Kind: domain.RegionShelf},
		{ID: "r-condenser", Name: "冷凝器", Kind: domain.RegionCondenser},
		{ID: "r-drain", Name: "排水口", Kind: domain.RegionDrain},
	}
	positions := []domain.ProbePosition{
		{ID: "p1", RegionID: "r-chamber", LoadLayer: 0},
		{ID: "p2", RegionID: "r-chamber", LoadLayer: 0},
		{ID: "p3", RegionID: "r-shelf", LoadLayer: 1},
		{ID: "p4", RegionID: "r-shelf", LoadLayer: 1},
	}
	return domain.ValidationPlan{
		ID:              "v1",
		StructureDigest: domain.StructureDigest(regions, positions),
		LoadDigest:      domain.LoadDigest(positions),
		Regions:         regions,
		Positions:       positions,
		ProbeSummaries: []domain.ProbeSummary{
			{ProbeID: "probe-1", PositionID: "p1", Certificate: "cert-1"},
			{ProbeID: "probe-2", PositionID: "p2", Certificate: "cert-2"},
			{ProbeID: "probe-3", PositionID: "p3", Certificate: "cert-3"},
			{ProbeID: "probe-4", PositionID: "p4", Certificate: "cert-4"},
		},
		Exposure: domain.ExposureRule{
			MinTemperature: 121000, // 121.000 °C
			MinPressure:    100000, // 100 kPa
			MaxPressure:    200000, // 200 kPa
			MinDuration:    60000,  // 60 s
		},
		SamplingInterval:   1000, // 1 s
		LethalityThreshold: 1_000_000,
	}
}

// temperatureProbe returns a temperature probe whose certificate covers a wide
// interval and whose range covers the exposure temperature.
func temperatureProbe(id domain.ProbeID, batch string) domain.ProbeLineage {
	return domain.ProbeLineage{
		ID:               id,
		Type:             domain.ProbeTemperature,
		RangeMin:         100000, // 100 °C
		RangeMax:         140000, // 140 °C
		Certificate:      "cert-" + string(id),
		CalibrationBatch: batch,
		ValidFrom:        0,
		ValidUntil:       1 << 40,
		Status:           domain.ProbeActive,
	}
}
