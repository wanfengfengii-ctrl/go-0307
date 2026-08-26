package tests

import (
	"context"
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/plan"
)

func TestLockPlanSuccess(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	gen, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-lock-1", Plan: p})
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if gen != 1 {
		t.Fatalf("generation = %d, want 1", gen)
	}

	got, err := svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.PlanLocked {
		t.Fatalf("status = %s, want locked", got.Status)
	}
	if got.StructureDigest != p.StructureDigest {
		t.Fatalf("structure digest mismatch")
	}
}

func TestLockPlanStaleDigest(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	p.StructureDigest = "stale-digest"
	_, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-stale", Plan: p})
	assertCode(t, err, domain.CodeValidationStale)

	// No partial plan must be persisted.
	if _, err := svc.Get(ctx, p.ID); err == nil {
		t.Fatalf("expected plan to be absent after failed lock")
	}
}

func TestLockPlanPositionUncovered(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	// A position referencing an unknown region is uncovered.
	p.Positions = append(p.Positions, domain.ProbePosition{ID: "p-orphan", RegionID: "r-ghost", LoadLayer: 0})
	p.StructureDigest = domain.StructureDigest(p.Regions, p.Positions)
	p.LoadDigest = domain.LoadDigest(p.Positions)
	_, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-uncovered", Plan: p})
	assertCode(t, err, domain.CodePositionUncovered)
}

func TestLockPlanDuplicateProbe(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	p.ProbeSummaries = append(p.ProbeSummaries, domain.ProbeSummary{ProbeID: "probe-1", PositionID: "p3", Certificate: "cert-dup"})
	_, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-dup", Plan: p})
	assertCode(t, err, domain.CodeDuplicateProbe)
}

func TestLockPlanSizeOverflow(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	for i := 0; i < 300; i++ {
		p.Positions = append(p.Positions, domain.ProbePosition{
			ID:        domain.PositionID("extra-" + string(rune('a'+i%26)) + string(rune('0'+i%26))),
			RegionID:  "r-chamber",
			LoadLayer: 0,
		})
	}
	p.StructureDigest = domain.StructureDigest(p.Regions, p.Positions)
	p.LoadDigest = domain.LoadDigest(p.Positions)
	_, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-overflow", Plan: p})
	assertCode(t, err, domain.CodeOverflow)
}

func TestLockPlanRelockIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := plan.NewService(st)

	p := fixedPlan()
	gen1, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-a", Plan: p})
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	gen2, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-a", Plan: p})
	if err != nil {
		t.Fatalf("idempotent relock: %v", err)
	}
	if gen1 != gen2 {
		t.Fatalf("idempotent relock generation = %d, want %d", gen2, gen1)
	}

	// A changed load creates a new generation.
	p2 := p
	p2.Positions = append(p2.Positions, domain.ProbePosition{ID: "p5", RegionID: "r-drain", LoadLayer: 0})
	p2.StructureDigest = domain.StructureDigest(p2.Regions, p2.Positions)
	p2.LoadDigest = domain.LoadDigest(p2.Positions)
	gen3, err := svc.Lock(ctx, plan.LockRequest{OperationID: "op-b", Plan: p2})
	if err != nil {
		t.Fatalf("new generation lock: %v", err)
	}
	if gen3 != gen1+1 {
		t.Fatalf("new generation = %d, want %d", gen3, gen1+1)
	}
}
