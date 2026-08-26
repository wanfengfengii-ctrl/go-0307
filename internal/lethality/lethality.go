// Package lethality is the "lethality and deviation" component. It performs
// fixed-point integration, uniformity and pressure-deviation determination,
// normalizes anomalies, computes the propagation closure, opens retest
// generations and coordinates dual review.
package lethality

import (
	"context"
	"errors"
	"strconv"

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
// when a sample is missing, out of range, or overflows. A retry with the same
// operation id replays the originally recorded result instead of recomputing,
// so a client that times out and retries observes identical, stable output.
func (s *Service) Calculate(ctx context.Context, req CalculateRequest) ([]domain.CalculationResult, error) {
	results, err := s.compute(ctx, req.CycleID)
	if err != nil {
		return nil, err
	}

	// The request digest summarizes the cycle being judged, so a retry of the
	// same operation against different content is rejected as a conflict.
	requestDigest := domain.Digest("calculate", req.CycleID, results[0].generation,
		len(results), AlgorithmVersion)

	var out []domain.CalculationResult
	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		// Idempotent replay of the same operation returns the recorded result.
		if rec, err := tx.GetIdempotency(ctx, req.OperationID); err == nil {
			if rec.RequestDigest != requestDigest {
				return domain.NewError(domain.CodeIdempotencyConflict, req.OperationID, false, "operation reused with different content")
			}
			generation := parseGeneration(rec.ResponseDigest)
			stored, err := tx.ListCalculations(ctx, req.CycleID, generation)
			if err != nil {
				return err
			}
			out = stored
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		for _, r := range results {
			if err := tx.InsertCalculation(ctx, req.CycleID, r.generation, r.CalculationResult); err != nil {
				return err
			}
		}
		if err := tx.RecordIdempotency(ctx, domain.IdempotencyRecord{
			OperationID:    req.OperationID,
			RequestDigest:  requestDigest,
			ResponseDigest: strconv.FormatInt(int64(results[0].generation), 10),
		}); err != nil {
			return err
		}

		out = make([]domain.CalculationResult, len(results))
		for i, r := range results {
			out[i] = r.CalculationResult
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseGeneration decodes a generation stored as an idempotency response
// digest back into a typed generation.
func parseGeneration(s string) domain.Generation {
	n, _ := strconv.ParseInt(s, 10, 64)
	return domain.Generation(n)
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
