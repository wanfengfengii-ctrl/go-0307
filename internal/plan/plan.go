// Package plan is the "validation plan and coverage rules" component. It
// manages equipment structure, load configuration, probe positions, phase
// parameters, calibration summaries and thresholds, and enforces version,
// position-coverage and integer-bound validation before producing an immutable
// generation.
package plan

import (
	"context"
	"errors"
	"strconv"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// Service is the concrete plan-locking boundary backed by the shared store.
type Service struct {
	store *store.Store
}

// NewService constructs a plan service over s.
func NewService(s *store.Store) *Service { return &Service{store: s} }

// LockRequest is the input for locking a validation plan.
type LockRequest struct {
	OperationID domain.OperationID
	Plan        domain.ValidationPlan
}

// Lock validates and freezes a plan, returning its generation. The validation
// is atomic: stale versions, missing positions, duplicate probes or integer
// overflow are rejected and leave no partial plan, region, position or event.
func (s *Service) Lock(ctx context.Context, req LockRequest) (domain.Generation, error) {
	plan := req.Plan
	canonicalStruct := domain.StructureDigest(plan.Regions, plan.Positions)
	canonicalLoad := domain.LoadDigest(plan.Positions)

	// Reject a claimed summary that does not match the submitted structure:
	// the client's digest is stale relative to the geometry it actually sent.
	if plan.StructureDigest != "" && plan.StructureDigest != canonicalStruct {
		return 0, domain.NewError(domain.CodeValidationStale, req.OperationID, false, "structure digest is stale")
	}
	if plan.LoadDigest != "" && plan.LoadDigest != canonicalLoad {
		return 0, domain.NewError(domain.CodeValidationStale, req.OperationID, false, "load digest is stale")
	}
	plan.StructureDigest = canonicalStruct
	plan.LoadDigest = canonicalLoad

	if err := ValidatePlan(plan); err != nil {
		if de, ok := err.(*domain.Error); ok {
			return 0, de.WithOperation(req.OperationID)
		}
		return 0, err
	}

	requestDigest := domain.Digest("lock", plan.ID, plan.StructureDigest, plan.LoadDigest,
		plan.Exposure.MinTemperature, plan.Exposure.MinPressure, plan.Exposure.MaxPressure,
		plan.Exposure.MinDuration, plan.SamplingInterval, plan.LethalityThreshold)

	var generation domain.Generation
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		// Idempotent replay of the same operation returns the recorded result.
		if rec, err := tx.GetIdempotency(ctx, req.OperationID); err == nil {
			if rec.RequestDigest != requestDigest {
				return domain.NewError(domain.CodeIdempotencyConflict, req.OperationID, false, "operation reused with different content")
			}
			generation = parseGeneration(rec.ResponseDigest)
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		existing, err := tx.GetPlan(ctx, plan.ID)
		switch {
		case err == nil:
			// Identical structure and load re-lock is idempotent.
			if existing.StructureDigest == plan.StructureDigest && existing.LoadDigest == plan.LoadDigest {
				generation = existing.Generation
			} else {
				generation = existing.Generation + 1
			}
		case errors.Is(err, store.ErrNotFound):
			generation = 1
		default:
			return err
		}

		plan.Generation = generation
		plan.Status = domain.PlanLocked
		if err := tx.LockPlan(ctx, plan); err != nil {
			return err
		}
		return tx.RecordIdempotency(ctx, domain.IdempotencyRecord{
			OperationID:    req.OperationID,
			RequestDigest:  requestDigest,
			ResponseDigest: strconv.FormatInt(int64(generation), 10),
		})
	})
	if err != nil {
		return 0, err
	}
	return generation, nil
}

// Get returns the locked plan for a validation id, or a NOT_FOUND error.
func (s *Service) Get(ctx context.Context, id domain.ValidationID) (domain.ValidationPlan, error) {
	var plan domain.ValidationPlan
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		plan, err = tx.GetPlan(ctx, id)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return plan, domain.NewError(domain.CodeNotFound, "", false, "validation plan not found")
	}
	return plan, err
}

// List returns every locked plan ordered by id.
func (s *Service) List(ctx context.Context) ([]domain.ValidationPlan, error) {
	var plans []domain.ValidationPlan
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		plans, err = tx.ListPlans(ctx)
		return err
	})
	return plans, err
}

func parseGeneration(s string) domain.Generation {
	n, _ := strconv.ParseInt(s, 10, 64)
	return domain.Generation(n)
}
