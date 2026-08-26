// Package cycle is the "cycle aggregation and collection" component. It
// maintains validation generations, the six-stage prefix, probe bindings,
// sample sequences and device calls as append-only events, and rejects
// out-of-order, skipped and stale-generation submissions.
package cycle

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// Service is the concrete cycle-collection boundary.
type Service struct {
	store *store.Store
}

// NewService constructs a cycle service over s.
func NewService(s *store.Store) *Service { return &Service{store: s} }

// StageRequest submits one stage transition. ValidationID identifies the plan
// the cycle is validating; it is captured on the first (preheat) stage.
type StageRequest struct {
	OperationID        domain.OperationID
	CycleID            domain.CycleID
	ValidationID       domain.ValidationID
	ExpectedGeneration domain.Generation
	Phase              domain.Phase
	LogicalTime        domain.LogicalTime
}

// Stage advances the cycle through the strict six-stage prefix. The first
// submitted stage must be preheat; every later stage must be exactly the next
// one in the canonical order. Skipping a stage, submitting a stale generation
// or a non-increasing logical time is rejected.
func (s *Service) Stage(ctx context.Context, req StageRequest) error {
	if !domain.ValidPhase(req.Phase) {
		return domain.NewError(domain.CodeInvalidPhase, req.OperationID, false, "unknown stage")
	}
	requestDigest := domain.Digest("stage", req.CycleID, req.Phase, req.LogicalTime)

	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if rec, err := tx.GetIdempotency(ctx, req.OperationID); err == nil {
			if rec.RequestDigest != requestDigest {
				return domain.NewError(domain.CodeIdempotencyConflict, req.OperationID, false, "operation reused with different content")
			}
			return nil // idempotent replay
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		state, err := s.loadState(ctx, tx, req.CycleID)
		if err != nil {
			return err
		}

		if state.exists {
			if req.ExpectedGeneration < state.generation {
				return domain.NewError(domain.CodeStaleGeneration, req.OperationID, false, "stage references an old generation")
			}
			if req.ExpectedGeneration > state.generation {
				return domain.NewError(domain.CodeGenerationMismatch, req.OperationID, false, "stage generation ahead of cycle")
			}
		} else {
			// A brand-new cycle adopts the requested generation.
			state.generation = req.ExpectedGeneration
		}

		expected := domain.PhaseAfter(state.phase)
		if state.phase == "" {
			expected = domain.PhasePreheat
		}
		if expected == "" {
			return domain.NewError(domain.CodeInvalidState, req.OperationID, false, "cycle already complete")
		}
		if req.Phase != expected {
			return domain.NewError(domain.CodeInvalidPhase, req.OperationID, false, "expected stage "+string(expected)+", got "+string(req.Phase))
		}
		if req.LogicalTime <= state.lastTime {
			return domain.NewError(domain.CodeNegativeInterval, req.OperationID, false, "logical time must increase")
		}

		seq := state.cursor + 1
		ev := domain.CycleEvent{
			CycleID:     req.CycleID,
			Generation:  state.generation,
			Sequence:    seq,
			Phase:       req.Phase,
			LogicalTime: req.LogicalTime,
			OperationID: req.OperationID,
			InputDigest: requestDigest,
		}
		if err := tx.AppendEvent(ctx, ev); err != nil {
			return err
		}

		status := domain.CycleOpen
		if req.Phase == domain.PhaseCooling {
			status = domain.CycleComplete
		}
		validationID := state.validationID
		if validationID == "" {
			validationID = req.ValidationID
		}
		if err := saveSnapshot(ctx, tx, req.CycleID, validationID, state.generation, seq, status); err != nil {
			return err
		}
		return tx.RecordIdempotency(ctx, domain.IdempotencyRecord{
			OperationID:    req.OperationID,
			RequestDigest:  requestDigest,
			ResponseDigest: string(req.Phase),
		})
	})
}

// Audit returns the append-only event timeline for a cycle, including audit
// events such as stale-generation samples.
func (s *Service) Audit(ctx context.Context, id domain.CycleID) ([]domain.CycleEvent, error) {
	var events []domain.CycleEvent
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		events, err = tx.ListEvents(ctx, id)
		return err
	})
	return events, err
}

// cycleState is the recoverable projection of a cycle at the current cursor.
type cycleState struct {
	exists       bool
	generation   domain.Generation
	validationID domain.ValidationID
	phase        domain.Phase
	cursor       domain.Sequence
	lastTime     domain.LogicalTime
	status       domain.CycleStatus
}

// loadState reads the cycle projection: its generation and cursor from the
// snapshot and its current phase and last logical time from the event timeline.
// A brand-new cycle has exists=false, generation zero, empty phase and zero
// cursor.
func (s *Service) loadState(ctx context.Context, tx *store.Tx, cycleID domain.CycleID) (cycleState, error) {
	state := cycleState{}
	snap, err := tx.GetSnapshot(ctx, cycleID)
	if err == nil {
		state.exists = true
		state.generation = snap.Generation
		state.validationID = snap.ValidationID
		state.cursor = snap.Cursor
		state.status = snap.Status
	} else if !errors.Is(err, store.ErrNotFound) {
		return state, err
	}

	phase, err := tx.CurrentPhase(ctx, cycleID, state.generation)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return state, err
	}
	if err == nil {
		state.phase = phase
	}

	lastTime, err := tx.LastLogicalTime(ctx, cycleID, state.generation)
	if err != nil {
		return state, err
	}
	state.lastTime = lastTime
	return state, nil
}

func saveSnapshot(ctx context.Context, tx *store.Tx, cycleID domain.CycleID, validationID domain.ValidationID, generation domain.Generation, cursor domain.Sequence, status domain.CycleStatus) error {
	return tx.SaveSnapshot(ctx, domain.CycleSnapshot{
		CycleID:      cycleID,
		ValidationID: validationID,
		Generation:   generation,
		Cursor:       cursor,
		Status:       status,
		Checksum:     domain.Checksum(cycleID, validationID, generation, cursor, status),
	})
}
