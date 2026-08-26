package tests

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_DeviceCallTransactionsHonorCancellation(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "canceled before transaction leaves no retry task",
			run: func(t *testing.T) {
				st := newStore(t)
				svc := cycle.NewService(st)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				if _, err := svc.RecordDeviceCall(ctx, cycle.DeviceCallRequest{
					OperationID: "canceled-before",
					Fault:       domain.FaultTimeout,
					LogicalTime: 10_000,
				}); !errors.Is(err, context.Canceled) {
					t.Fatalf("RecordDeviceCall error = %v, want context.Canceled", err)
				}

				calls, err := svc.DeviceCalls(context.Background())
				if err != nil {
					t.Fatalf("DeviceCalls: %v", err)
				}
				if len(calls) != 0 {
					t.Fatalf("device calls = %#v, want none", calls)
				}
			},
		},
		{
			name: "canceled during insert rolls retry task back",
			run: func(t *testing.T) {
				st := newStore(t)
				ctx, cancel := context.WithCancel(context.Background())
				err := st.InTx(ctx, func(tx *store.Tx) error {
					if err := tx.InsertDeviceCall(ctx, domain.DeviceCall{
						OperationID: "canceled-insert",
						Fault:       domain.FaultDisconnect,
						Retries:     1,
						NextRetryAt: 11_000,
					}); err != nil {
						return err
					}
					cancel()
					return nil
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("InTx error = %v, want context.Canceled", err)
				}

				svc := cycle.NewService(st)
				calls, err := svc.DeviceCalls(context.Background())
				if err != nil {
					t.Fatalf("DeviceCalls: %v", err)
				}
				if len(calls) != 0 {
					t.Fatalf("device calls = %#v, want canceled insert rolled back", calls)
				}
			},
		},
		{
			name: "canceled during update does not advance retry",
			run: func(t *testing.T) {
				st := newStore(t)
				svc := cycle.NewService(st)
				initial, err := svc.RecordDeviceCall(context.Background(), cycle.DeviceCallRequest{
					OperationID: "canceled-update",
					Fault:       domain.FaultTimeout,
					LogicalTime: 20_000,
				})
				if err != nil {
					t.Fatalf("seed device call: %v", err)
				}

				ctx, cancel := context.WithCancel(context.Background())
				err = st.InTx(ctx, func(tx *store.Tx) error {
					updated := initial
					updated.Retries++
					updated.NextRetryAt = 99_000
					if err := tx.UpdateDeviceCall(ctx, updated); err != nil {
						return err
					}
					cancel()
					return nil
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("InTx error = %v, want context.Canceled", err)
				}

				calls, err := svc.DeviceCalls(context.Background())
				if err != nil {
					t.Fatalf("DeviceCalls: %v", err)
				}
				if len(calls) != 1 || calls[0] != initial {
					t.Fatalf("device calls = %#v, want unchanged %#v", calls, initial)
				}
			},
		},
		{
			name: "committed retry uses logical clock exhausts and survives restart",
			run: func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "device-calls.db")
				st, err := store.Open(path)
				if err != nil {
					t.Fatalf("open store: %v", err)
				}
				svc := cycle.NewService(st)

				var got domain.DeviceCall
				for attempt := 1; attempt <= cycle.MaxDeviceRetries+1; attempt++ {
					logicalTime := domain.LogicalTime(attempt * 10_000)
					got, err = svc.RecordDeviceCall(ctx, cycle.DeviceCallRequest{
						OperationID: "committed",
						Fault:       domain.FaultDrift,
						LogicalTime: logicalTime,
					})
					if err != nil {
						t.Fatalf("attempt %d: %v", attempt, err)
					}
					wantNext := logicalTime + cycle.RetryDelayMillis*domain.LogicalTime(attempt)
					if got.Retries != attempt || got.NextRetryAt != wantNext {
						t.Fatalf("attempt %d call = %#v, want retries=%d next_retry_at=%d", attempt, got, attempt, wantNext)
					}
				}
				if got.Receipt != "exhausted" {
					t.Fatalf("receipt = %q, want exhausted", got.Receipt)
				}
				if err := st.Close(); err != nil {
					t.Fatalf("close store: %v", err)
				}

				st, err = store.Open(path)
				if err != nil {
					t.Fatalf("reopen store: %v", err)
				}
				defer st.Close()
				calls, err := cycle.NewService(st).DeviceCalls(ctx)
				if err != nil {
					t.Fatalf("DeviceCalls after restart: %v", err)
				}
				if len(calls) != 1 || calls[0] != got {
					t.Fatalf("device calls after restart = %#v, want %#v", calls, got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
