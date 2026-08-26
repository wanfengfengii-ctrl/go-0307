package lethality

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/fixed"
	"lyophilizer-sterilization-validation/internal/store"
)

// computedResult pairs a calculation result with the cycle generation it was
// computed against, so the write step can target the correct generation.
type computedResult struct {
	domain.CalculationResult
	generation domain.Generation
}

// compute reads the cycle's snapshot, plan, bindings and samples at a single
// cursor and produces one deterministic result per bound temperature position.
// It returns an error without any side effects on the first missing sample,
// out-of-range value or overflow.
func (s *Service) compute(ctx context.Context, cycleID domain.CycleID) ([]computedResult, error) {
	var (
		generation   domain.Generation
		validationID domain.ValidationID
		plan         domain.ValidationPlan
		bindings     []domain.ProbeBinding
		probeIDs     []domain.ProbeID
	)
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		snap, err := tx.GetSnapshot(ctx, cycleID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "cycle not found")
		}
		if err != nil {
			return err
		}
		generation = snap.Generation
		validationID = snap.ValidationID

		plan, err = tx.GetPlan(ctx, validationID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "validation plan not found")
		}
		if err != nil {
			return err
		}

		bindings, err = tx.ListBindings(ctx, generation)
		if err != nil {
			return err
		}
		probeIDs, err = tx.ListSampleSeqs(ctx, cycleID, generation)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Map probe -> position so each sample series is attributed to a location.
	probeToPosition := make(map[domain.ProbeID]domain.PositionID, len(bindings))
	for _, b := range bindings {
		probeToPosition[b.ProbeID] = b.PositionID
	}
	// Every declared position, so a bound probe must map to a planned location.
	declared := make(map[domain.PositionID]bool, len(plan.Positions))
	for _, pos := range plan.Positions {
		declared[pos.ID] = true
	}

	var results []computedResult

	for _, probeID := range probeIDs {
		positionID, ok := probeToPosition[probeID]
		if !ok {
			return nil, domain.NewError(domain.CodeInvalidState, "", false, "sample probe is not bound to a position")
		}
		if !declared[positionID] {
			return nil, domain.NewError(domain.CodeInvalidState, "", false, "sample position is not part of the plan")
		}

		samples, err := s.samples(ctx, cycleID, generation, probeID)
		if err != nil {
			return nil, err
		}
		if len(samples) < 2 {
			return nil, domain.NewError(domain.CodeMissingSample, "", false, "position "+string(positionID)+" has fewer than two samples")
		}

		points := make([]fixed.Point, len(samples))
		readings := make([]int64, len(samples))
		for i, smp := range samples {
			points[i] = fixed.Point{Value: smp.Reading, Time: int64(smp.LogicalTime)}
			readings[i] = smp.Reading
		}

		// Accumulated exposure is the temperature-time area (milli-celsius ×
		// milliseconds), integrated from the raw temperature series.
		accumulated, err := fixed.Integrate(points, 0)
		if err != nil {
			return nil, err
		}
		lethality, err := fixed.BaseEquivalent(accumulated, ReferenceTemperature*millisecondsPerMinute, domain.LethalityScale)
		if err != nil {
			return nil, err
		}
		minTemp, err := fixed.MinValue(readings)
		if err != nil {
			return nil, err
		}
		swing, err := fixed.Uniformity(readings)
		if err != nil {
			return nil, err
		}

		results = append(results, computedResult{
			CalculationResult: domain.CalculationResult{
				PositionID:        positionID,
				Accumulated:       accumulated,
				Lethality:         lethality,
				MinTemperature:    minTemp,
				Uniformity:        swing,
				PressureDeviation: 0,
				InputFrom:         samples[0].LogicalTime,
				InputTo:           samples[len(samples)-1].LogicalTime,
				AlgorithmVersion:  AlgorithmVersion,
			},
			generation: generation,
		})
	}

	if len(results) == 0 {
		return nil, domain.NewError(domain.CodeMissingSample, "", false, "no sample data for calculation")
	}
	return results, nil
}

// samples returns a probe's samples ordered by sequence.
func (s *Service) samples(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, probeID domain.ProbeID) ([]domain.Sample, error) {
	var out []domain.Sample
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListSamplesByProbe(ctx, cycleID, generation, probeID)
		return err
	})
	return out, err
}
