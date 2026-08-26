// Package lethality is the "lethality and deviation" component. It performs
// fixed-point integration, uniformity and pressure-deviation determination,
// normalizes anomalies, computes the propagation closure, opens retest
// generations and coordinates dual review.
package lethality

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// AlgorithmVersion identifies the deterministic calculation algorithm, so
// results are reproducible and auditable.
const AlgorithmVersion = "trapezoid-v1"

// ReferenceTemperature is the base temperature (121.1 °C in milli-celsius)
// used to normalize accumulated thermal dose into equivalent lethality.
const ReferenceTemperature int64 = 121100

// millisecondsPerMinute converts a millisecond duration into minutes for the
// base-equivalent normalization denominator.
const millisecondsPerMinute int64 = 60000

// Service is the concrete lethality-and-deviation boundary.
type Service struct {
	store *store.Store
}

// NewService constructs a lethality service over s.
func NewService(s *store.Store) *Service { return &Service{store: s} }

// CalculateRequest runs the deterministic cycle determination.
type CalculateRequest struct {
	OperationID domain.OperationID
	CycleID     domain.CycleID
}

// Calculate runs the deterministic judgment for a cycle over one event cursor
// and appends results atomically, refusing to write any partial position result
// when a sample is missing, out of range, or overflows.
func (s *Service) Calculate(ctx context.Context, req CalculateRequest) ([]domain.CalculationResult, error) {
	results, err := s.compute(ctx, req.CycleID)
	if err != nil {
		return nil, err
	}

	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		for _, r := range results {
			if err := tx.InsertCalculation(ctx, req.CycleID, r.generation, r.CalculationResult); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]domain.CalculationResult, len(results))
	for i, r := range results {
		out[i] = r.CalculationResult
	}
	return out, nil
}

// Results returns the computed results with input intervals for a cycle.
func (s *Service) Results(ctx context.Context, id domain.CycleID) ([]domain.CalculationResult, error) {
	generation, err := s.currentGeneration(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []domain.CalculationResult
	err = s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListCalculations(ctx, id, generation)
		return err
	})
	return out, err
}

// currentGeneration returns the cycle's active generation from its snapshot.
func (s *Service) currentGeneration(ctx context.Context, id domain.CycleID) (domain.Generation, error) {
	var generation domain.Generation
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		snap, err := tx.GetSnapshot(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "cycle not found")
		}
		if err != nil {
			return err
		}
		generation = snap.Generation
		return nil
	})
	return generation, err
}
