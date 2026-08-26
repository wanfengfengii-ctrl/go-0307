package cycle

import (
	"context"
	"errors"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// Fixed retry policy for device failures: a bounded number of retries, each
// scheduled a fixed logical delay after the previous attempt. The policy never
// depends on wall time, so the retry queue is reproducible across restarts.
const (
	MaxDeviceRetries = 3
	RetryDelayMillis = domain.LogicalTime(1000)
)

// DeviceCallRequest records one device invocation outcome (a fault or success).
type DeviceCallRequest struct {
	OperationID domain.OperationID
	Fault       domain.FaultClass
	LogicalTime domain.LogicalTime
}

// RecordDeviceCall applies the fixed retry policy to a device invocation. A
// first fault creates a retry task with one retry scheduled; repeated faults
// advance the retry counter and next retry time until the bound is exhausted.
// The transaction is bound to the request context: a canceled request rolls the
// write back so no ghost retry task is left behind, instead of committing the
// record and then reporting cancellation to the caller.
func (s *Service) RecordDeviceCall(ctx context.Context, req DeviceCallRequest) (domain.DeviceCall, error) {
	if req.Fault == "" {
		return domain.DeviceCall{}, domain.NewError(domain.CodeInvalidPlan, req.OperationID, false, "device call fault is empty")
	}

	var out domain.DeviceCall
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetDeviceCall(ctx, req.OperationID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			out = domain.DeviceCall{
				OperationID: req.OperationID,
				Fault:       req.Fault,
				Retries:     1,
				NextRetryAt: req.LogicalTime + RetryDelayMillis,
				Receipt:     "",
			}
			return tx.InsertDeviceCall(ctx, out)
		case err != nil:
			return err
		default:
			out = existing
			out.Fault = req.Fault
			out.Retries = existing.Retries + 1
			out.NextRetryAt = req.LogicalTime + RetryDelayMillis*domain.LogicalTime(out.Retries)
			if out.Retries > MaxDeviceRetries {
				out.Receipt = "exhausted"
			}
			return tx.UpdateDeviceCall(ctx, out)
		}
	})
	if err != nil {
		// A rolled-back write (including a client cancellation that aborted the
		// transaction) leaves no retry task behind; do not surface the partially
		// built record, since it was never persisted.
		return domain.DeviceCall{}, err
	}
	return out, nil
}

// DeviceCalls returns every recorded device invocation ordered by operation.
func (s *Service) DeviceCalls(ctx context.Context) ([]domain.DeviceCall, error) {
	var out []domain.DeviceCall
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListDeviceCalls(ctx)
		return err
	})
	return out, err
}
