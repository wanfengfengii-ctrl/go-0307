package tests

import (
	"context"
	"path/filepath"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestRestartRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lyo.db")

	// First run: produce a snapshot, a lease and a retry queue.
	func() {
		st, err := store.Open(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer st.Close()

		svc := cycle.NewService(st)
		for i, ph := range allPhases {
			if err := svc.Stage(ctx, cycle.StageRequest{
				OperationID:        domain.OperationID("op-" + string(ph)),
				CycleID:            "c1",
				ValidationID:       "v1",
				ExpectedGeneration: 1,
				Phase:              ph,
				LogicalTime:        domain.LogicalTime(1000 * (i + 1)),
			}); err != nil {
				t.Fatalf("stage %s: %v", ph, err)
			}
		}

		for _, f := range []domain.FaultClass{domain.FaultDisconnect, domain.FaultTimeout, domain.FaultMalformed, domain.FaultDrift} {
			if _, err := svc.RecordDeviceCall(ctx, cycle.DeviceCallRequest{
				OperationID: domain.OperationID("dev-" + string(f)),
				Fault:       f,
				LogicalTime: 10000,
			}); err != nil {
				t.Fatalf("record device call: %v", err)
			}
		}
	}()

	// Second run: reopen and verify the projection, retry queue and checksums.
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()

	if n, err := st.Recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	} else if n != 1 {
		t.Fatalf("recovered %d snapshots, want 1", n)
	}

	var snap domain.CycleSnapshot
	if err := st.Read(ctx, func(tx *store.Tx) error {
		var err error
		snap, err = tx.GetSnapshot(ctx, "c1")
		return err
	}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Status != domain.CycleComplete {
		t.Fatalf("snapshot status = %s, want complete", snap.Status)
	}
	if snap.Cursor != 6 {
		t.Fatalf("snapshot cursor = %d, want 6", snap.Cursor)
	}

	svc := cycle.NewService(st)
	calls, err := svc.DeviceCalls(ctx)
	if err != nil {
		t.Fatalf("device calls: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("device calls = %d, want 4", len(calls))
	}
	for _, c := range calls {
		if c.Retries != 1 {
			t.Fatalf("device call %s retries = %d, want 1", c.OperationID, c.Retries)
		}
	}
}

func TestDeviceCallRetryExhaustion(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)

	var last domain.DeviceCall
	for i := 0; i < cycle.MaxDeviceRetries+2; i++ {
		c, err := svc.RecordDeviceCall(ctx, cycle.DeviceCallRequest{
			OperationID: "op-dev",
			Fault:       domain.FaultTimeout,
			LogicalTime: domain.LogicalTime(1000 * (i + 1)),
		})
		if err != nil {
			t.Fatalf("record device call %d: %v", i, err)
		}
		last = c
	}
	if last.Receipt != "exhausted" {
		t.Fatalf("receipt = %q, want exhausted", last.Receipt)
	}
	if last.Retries != cycle.MaxDeviceRetries+2 {
		t.Fatalf("retries = %d, want %d", last.Retries, cycle.MaxDeviceRetries+2)
	}
}
