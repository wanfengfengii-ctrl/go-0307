package tests

import (
	"context"
	"sync"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/lethality"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

// setupRetest locks a plan and binds four probes with two calibration batches.
func setupRetest(t *testing.T, ctx context.Context, st *store.Store) *lethality.Service {
	t.Helper()
	planSvc := plan.NewService(st)
	if _, err := planSvc.Lock(ctx, plan.LockRequest{OperationID: "op-lock", Plan: fixedPlan()}); err != nil {
		t.Fatalf("lock: %v", err)
	}

	probeSvc := probe.NewService(st)
	batches := map[domain.ProbeID]string{
		"probe-1": "batch-a",
		"probe-2": "batch-a",
		"probe-3": "batch-b",
		"probe-4": "batch-b",
	}
	for id, batch := range batches {
		registerProbes(t, st, probeSvc, temperatureProbe(id, batch))
		if err := probeSvc.Bind(ctx, probe.BindRequest{
			OperationID: domain.OperationID("op-bind-" + string(id)), ProbeID: id,
			PositionID: domain.PositionID("p" + string(id[len(id)-1])), Generation: 1,
			ValidFrom: 0, ValidUntil: 1 << 40, RangeMin: 121000, RangeMax: 122000,
		}); err != nil {
			t.Fatalf("bind %s: %v", id, err)
		}
	}

	// Create a cycle snapshot so retest propagation has a generation and
	// validation id to read.
	cycleSvc := cycle.NewService(st)
	if err := cycleSvc.Stage(ctx, cycle.StageRequest{
		OperationID: "op-preheat", CycleID: "c1", ValidationID: "v1",
		ExpectedGeneration: 1, Phase: domain.PhasePreheat, LogicalTime: 1000,
	}); err != nil {
		t.Fatalf("preheat: %v", err)
	}
	return lethality.NewService(st)
}

func TestRetestPropagationDedup(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := setupRetest(t, ctx, st)

	gen, err := svc.OpenRetest(ctx, lethality.RetestRequest{
		OperationID: "op-retest", CycleID: "c1", Sources: []string{"p1"},
	})
	if err != nil {
		t.Fatalf("open retest: %v", err)
	}
	if gen != 2 {
		t.Fatalf("retest generation = %d, want 2", gen)
	}

	members, err := svc.RetestMembers(ctx, "c1", gen)
	if err != nil {
		t.Fatalf("retest members: %v", err)
	}
	// p1 and p2 share the chamber region (channel) and load layer 0, and their
	// probes share calibration batch-a, so both are propagated; p3/p4 are not.
	if len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
	if members[0].Position != "p1" || members[1].Position != "p2" {
		t.Fatalf("members = %+v, want p1 and p2 sorted", members)
	}
}

func TestRetestConcurrentSingleGeneration(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := setupRetest(t, ctx, st)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var gen1, gen2 domain.Generation
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		gen1, err1 = svc.OpenRetest(ctx, lethality.RetestRequest{OperationID: "op-r1", CycleID: "c1", Sources: []string{"p1"}})
	}()
	go func() {
		defer wg.Done()
		<-start
		gen2, err2 = svc.OpenRetest(ctx, lethality.RetestRequest{OperationID: "op-r2", CycleID: "c1", Sources: []string{"p1"}})
	}()
	close(start)
	wg.Wait()

	if err1 != nil || err2 != nil {
		t.Fatalf("concurrent retest errors: %v, %v", err1, err2)
	}
	if gen1 != gen2 {
		t.Fatalf("concurrent retest generations differ: %d vs %d", gen1, gen2)
	}
	if gen1 != 2 {
		t.Fatalf("retest generation = %d, want 2", gen1)
	}
}
