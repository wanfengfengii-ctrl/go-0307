package plan

import (
	"lyophilizer-sterilization-validation/internal/domain"
)

// Validation limits enforced at lock time. Exceeding them is an integer-bound
// ("size overflow") failure.
const (
	MaxRegions   = 64
	MaxPositions = 256
	MaxLoadLayer = 64
)

// ValidatePlan performs the geometry, coverage, uniqueness and integer-bound
// checks required before a plan can be frozen. It returns a *domain.Error with
// a stable code and no side effects.
func ValidatePlan(p domain.ValidationPlan) error {
	if p.ID == "" {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "validation id is empty")
	}
	if len(p.Regions) == 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "no chamber regions declared")
	}
	if len(p.Regions) > MaxRegions {
		return domain.NewError(domain.CodeOverflow, "", false, "region count exceeds limit")
	}
	if len(p.Positions) == 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "no probe positions declared")
	}
	if len(p.Positions) > MaxPositions {
		return domain.NewError(domain.CodeOverflow, "", false, "position count exceeds limit")
	}

	regionSet := make(map[domain.RegionID]bool, len(p.Regions))
	for _, r := range p.Regions {
		if r.ID == "" {
			return domain.NewError(domain.CodeInvalidPlan, "", false, "region id is empty")
		}
		if regionSet[r.ID] {
			return domain.NewError(domain.CodeDuplicateKey, "", false, "duplicate region id "+string(r.ID))
		}
		regionSet[r.ID] = true
	}

	// Position coverage: every position must reference a declared region, and
	// every position id must be unique.
	positionSet := make(map[domain.PositionID]bool, len(p.Positions))
	regionCovered := make(map[domain.RegionID]bool)
	for _, pos := range p.Positions {
		if pos.ID == "" {
			return domain.NewError(domain.CodeInvalidPlan, "", false, "position id is empty")
		}
		if positionSet[pos.ID] {
			return domain.NewError(domain.CodeDuplicateKey, "", false, "duplicate position id "+string(pos.ID))
		}
		positionSet[pos.ID] = true
		if !regionSet[pos.RegionID] {
			return domain.NewError(domain.CodePositionUncovered, "", false, "position "+string(pos.ID)+" references unknown region "+string(pos.RegionID))
		}
		if pos.LoadLayer < 0 || pos.LoadLayer > MaxLoadLayer {
			return domain.NewError(domain.CodeOverflow, "", false, "load layer out of range for position "+string(pos.ID))
		}
		regionCovered[pos.RegionID] = true
	}

	// Coverage: critical chamber and shelf regions must carry at least one
	// position, otherwise the load is missing coverage.
	for _, r := range p.Regions {
		if (r.Kind == domain.RegionChamber || r.Kind == domain.RegionShelf) && !regionCovered[r.ID] {
			return domain.NewError(domain.CodePositionUncovered, "", false, "region "+string(r.ID)+" has no probe position")
		}
	}

	// Probe summary uniqueness: a probe may appear only once in the plan.
	probeSet := make(map[domain.ProbeID]bool, len(p.ProbeSummaries))
	for _, ps := range p.ProbeSummaries {
		if ps.ProbeID == "" {
			return domain.NewError(domain.CodeInvalidPlan, "", false, "probe summary has empty probe id")
		}
		if probeSet[ps.ProbeID] {
			return domain.NewError(domain.CodeDuplicateProbe, "", false, "duplicate probe "+string(ps.ProbeID))
		}
		probeSet[ps.ProbeID] = true
		if !positionSet[ps.PositionID] {
			return domain.NewError(domain.CodePositionUncovered, "", false, "probe "+string(ps.ProbeID)+" references unknown position "+string(ps.PositionID))
		}
	}

	// Integer boundaries for exposure, sampling and threshold.
	if err := validateExposure(p.Exposure); err != nil {
		return err
	}
	if p.SamplingInterval <= 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "sampling interval must be positive")
	}
	if p.LethalityThreshold <= 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "lethality threshold must be positive")
	}
	return nil
}

func validateExposure(e domain.ExposureRule) error {
	if e.MinTemperature <= 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "exposure minimum temperature must be positive")
	}
	if e.MinPressure <= 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "exposure minimum pressure must be positive")
	}
	if e.MaxPressure < e.MinPressure {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "exposure maximum pressure below minimum")
	}
	if e.MinDuration <= 0 {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "exposure minimum duration must be positive")
	}
	return nil
}
