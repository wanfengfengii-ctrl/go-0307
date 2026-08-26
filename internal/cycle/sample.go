package cycle

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// ChannelResource returns the acquisition-channel resource id for a position.
// Probes bound to a position sample through this channel's lease, which is the
// mutual-exclusion point between the probe matrix and the sample stream.
func ChannelResource(positionID domain.PositionID) domain.ResourceID {
	return domain.ResourceID("channel:" + string(positionID))
}

// SampleRequest submits one integer sample.
type SampleRequest struct {
	OperationID        domain.OperationID
	CycleID            domain.CycleID
	ExpectedGeneration domain.Generation
	Token              domain.TokenID
	Sample             domain.Sample
}

// Sample appends a sample after validating the stage, sequence, lease,
// generation and device receipt. A stale generation is recorded as an audit
// event only and never advances the aggregate.
func (s *Service) Sample(ctx context.Context, req SampleRequest) error {
	sample := req.Sample
	requestDigest := domain.Digest("sample", sample.ProbeID, sample.Sequence, sample.LogicalTime, sample.Reading, sample.DeviceReceipt)

	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if rec, err := tx.GetIdempotency(ctx, req.OperationID); err == nil {
			if rec.RequestDigest != requestDigest {
				return domain.NewError(domain.CodeIdempotencyConflict, req.OperationID, false, "operation reused with different content")
			}
			return nil // idempotent replay of an identical sample
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		state, err := s.loadState(ctx, tx, req.CycleID)
		if err != nil {
			return err
		}

		// A late sample from an old generation only enters the audit timeline.
		if req.ExpectedGeneration < state.generation {
			return s.recordAudit(ctx, tx, req.CycleID, state, domain.Digest("stale-sample", requestDigest))
		}
		if req.ExpectedGeneration > state.generation {
			return domain.NewError(domain.CodeGenerationMismatch, req.OperationID, false, "sample generation ahead of cycle")
		}
		if state.phase != domain.PhaseExposure {
			return domain.NewError(domain.CodeInvalidState, req.OperationID, false, "samples are only accepted during exposure")
		}

		// Lease and binding validation: the token must be active and bound to
		// this probe's acquisition channel.
		lease, err := tx.GetLease(ctx, req.Token)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeLeaseExpired, req.OperationID, false, "lease token unknown")
		}
		if err != nil {
			return err
		}
		if lease.ValidUntil <= sample.LogicalTime || lease.ValidFrom > sample.LogicalTime {
			return domain.NewError(domain.CodeLeaseExpired, req.OperationID, false, "lease token expired at sample time")
		}
		// The token must be for the current generation: a lease acquired for an
		// older generation does not authorize writes to this generation's
		// evidence, even when it covers the same acquisition channel.
		if lease.Generation != state.generation {
			return domain.NewError(domain.CodeLeaseExpired, req.OperationID, false, "lease token is from a different generation")
		}
		binding, err := tx.FindBinding(ctx, sample.ProbeID, state.generation)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeInvalidState, req.OperationID, false, "probe is not bound for this generation")
		}
		if err != nil {
			return err
		}
		if lease.ResourceID != ChannelResource(binding.PositionID) {
			return domain.NewError(domain.CodeLeaseExpired, req.OperationID, false, "lease token does not match probe channel")
		}

		// Per-probe strictly increasing sequence and logical time.
		nextSeq, err := tx.NextSampleSeq(ctx, req.CycleID, state.generation, sample.ProbeID)
		if err != nil {
			return err
		}
		if sample.Sequence < nextSeq+1 {
			return domain.NewError(domain.CodeDuplicateKey, req.OperationID, false, "duplicate or out-of-order sample sequence")
		}
		if sample.Sequence > nextSeq+1 {
			return domain.NewError(domain.CodeSequenceGap, req.OperationID, false, "sample sequence gap")
		}
		lastTime, err := tx.LastSampleLogicalTime(ctx, req.CycleID, state.generation, sample.ProbeID)
		if err != nil {
			return err
		}
		if sample.LogicalTime <= lastTime {
			return domain.NewError(domain.CodeNegativeInterval, req.OperationID, false, "sample logical time must increase")
		}

		if err := tx.InsertSample(ctx, req.CycleID, state.generation, sample); err != nil {
			return err
		}
		return tx.RecordIdempotency(ctx, domain.IdempotencyRecord{
			OperationID:    req.OperationID,
			RequestDigest:  requestDigest,
			ResponseDigest: "recorded",
		})
	})
}

// recordAudit appends a non-advancing audit event so a stale-generation sample
// is visible in the timeline but does not change the current projection.
func (s *Service) recordAudit(ctx context.Context, tx *store.Tx, cycleID domain.CycleID, state cycleState, digest string) error {
	seq := state.cursor + 1
	ev := domain.CycleEvent{
		CycleID:     cycleID,
		Generation:  state.generation,
		Sequence:    seq,
		Phase:       state.phase,
		LogicalTime: state.lastTime,
		InputDigest: digest,
		Audit:       true,
	}
	if err := tx.AppendEvent(ctx, ev); err != nil {
		return err
	}
	return saveSnapshot(ctx, tx, cycleID, state.validationID, state.generation, seq, state.status)
}
