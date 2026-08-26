package tests

import (
	"context"
	"errors"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_LeaseRenewalAtomicity(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "conflicting renewal preserves original token and adjacent lease",
			run: func(t *testing.T) {
				ctx := context.Background()
				st := newStore(t)
				probeSvc := probe.NewService(st)
				cycleSvc := cycle.NewService(st)
				resource := cycle.ChannelResource("p1")

				registerProbes(t, st, probeSvc, temperatureProbe("probe-1", "batch-a"))
				if err := probeSvc.Bind(ctx, probe.BindRequest{
					OperationID: "model-bind", ProbeID: "probe-1", PositionID: "p1", Generation: 1,
					ValidFrom: 0, ValidUntil: 7000, RangeMin: 121000, RangeMax: 122000,
				}); err != nil {
					t.Fatalf("bind probe: %v", err)
				}

				first, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
					OperationID: "model-first", ResourceID: resource, Generation: 1,
					ValidFrom: 0, ValidUntil: 5000,
				})
				if err != nil {
					t.Fatalf("acquire first lease: %v", err)
				}
				second, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
					OperationID: "model-second", ResourceID: resource, Generation: 1,
					ValidFrom: 5000, ValidUntil: 7000,
				})
				if err != nil {
					t.Fatalf("acquire adjacent lease: %v", err)
				}

				err = probeSvc.Renew(ctx, first, 6000)
				assertCode(t, err, domain.CodeLeaseConflict)

				var firstLease, secondLease domain.ResourceLease
				if err := st.Read(ctx, func(tx *store.Tx) error {
					var readErr error
					firstLease, readErr = tx.GetLease(ctx, first)
					if readErr != nil {
						return readErr
					}
					secondLease, readErr = tx.GetLease(ctx, second)
					return readErr
				}); err != nil {
					t.Fatalf("read leases after rejected renewal: %v", err)
				}
				if firstLease.Token != first || firstLease.ValidFrom != 0 || firstLease.ValidUntil != 5000 {
					t.Fatalf("first lease changed after conflict: %+v", firstLease)
				}
				if secondLease.Token != second || secondLease.ValidFrom != 5000 || secondLease.ValidUntil != 7000 {
					t.Fatalf("adjacent lease changed after conflict: %+v", secondLease)
				}

				for i, phase := range []domain.Phase{
					domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup, domain.PhaseExposure,
				} {
					if err := cycleSvc.Stage(ctx, cycle.StageRequest{
						OperationID: domain.OperationID("model-stage-" + string(phase)),
						CycleID:     "model-cycle", ValidationID: "v1", ExpectedGeneration: 1,
						Phase: phase, LogicalTime: domain.LogicalTime((i + 1) * 1000),
					}); err != nil {
						t.Fatalf("stage %s: %v", phase, err)
					}
				}
				if err := cycleSvc.Sample(ctx, cycle.SampleRequest{
					OperationID: "model-sample", CycleID: "model-cycle", ExpectedGeneration: 1, Token: first,
					Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: 4500, Reading: 121000, Valid: true},
				}); err != nil {
					t.Fatalf("sample in original lease interval after conflict: %v", err)
				}
				if err := probeSvc.Release(ctx, first); err != nil {
					t.Fatalf("release original token after conflict: %v", err)
				}
			},
		},
		{
			name: "successful renewal retains token and normal release removes it",
			run: func(t *testing.T) {
				ctx := context.Background()
				st := newStore(t)
				svc := probe.NewService(st)
				token, err := svc.Acquire(ctx, probe.LeaseRequest{
					OperationID: "model-renew", ResourceID: "channel:p2", Generation: 3,
					ValidFrom: 10, ValidUntil: 20,
				})
				if err != nil {
					t.Fatalf("acquire lease: %v", err)
				}
				if err := svc.Renew(ctx, token, 30); err != nil {
					t.Fatalf("renew lease: %v", err)
				}
				var renewed domain.ResourceLease
				if err := st.Read(ctx, func(tx *store.Tx) error {
					var readErr error
					renewed, readErr = tx.GetLease(ctx, token)
					return readErr
				}); err != nil {
					t.Fatalf("read renewed lease: %v", err)
				}
				if renewed.Token != token || renewed.ValidFrom != 10 || renewed.ValidUntil != 30 {
					t.Fatalf("renewed lease = %+v, want same token over [10,30)", renewed)
				}
				if err := svc.Release(ctx, token); err != nil {
					t.Fatalf("release renewed lease: %v", err)
				}
				if err := st.Read(ctx, func(tx *store.Tx) error {
					_, readErr := tx.GetLease(ctx, token)
					return readErr
				}); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("released lease lookup error = %v, want store.ErrNotFound", err)
				}
				assertCode(t, svc.Renew(ctx, "unknown-token", 30), domain.CodeNotFound)
				assertCode(t, svc.Release(ctx, "unknown-token"), domain.CodeNotFound)
			},
		},
		{
			name: "cancelled renewal leaves interval unchanged",
			run: func(t *testing.T) {
				ctx := context.Background()
				st := newStore(t)
				svc := probe.NewService(st)
				token, err := svc.Acquire(ctx, probe.LeaseRequest{
					OperationID: "model-cancel", ResourceID: "channel:p3", Generation: 1,
					ValidFrom: 100, ValidUntil: 200,
				})
				if err != nil {
					t.Fatalf("acquire lease: %v", err)
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if err := svc.Renew(cancelled, token, 300); err == nil {
					t.Fatal("renew with cancelled context succeeded")
				}
				var lease domain.ResourceLease
				if err := st.Read(ctx, func(tx *store.Tx) error {
					var readErr error
					lease, readErr = tx.GetLease(ctx, token)
					return readErr
				}); err != nil {
					t.Fatalf("read lease after cancelled renewal: %v", err)
				}
				if lease.ValidFrom != 100 || lease.ValidUntil != 200 {
					t.Fatalf("lease changed after cancelled renewal: %+v", lease)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
