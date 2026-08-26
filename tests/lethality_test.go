package tests

import (
	"context"
	"math"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/fixed"
	"lyophilizer-sterilization-validation/internal/lethality"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
)

func TestIntegrateTrapezoidFixedSequence(t *testing.T) {
	// Excess temperatures 0, 1000, 0 milli-celsius over two 1000 ms segments.
	points := []fixed.Point{
		{Value: 121000, Time: 1000},
		{Value: 122000, Time: 2000},
		{Value: 121000, Time: 3000},
	}
	got, err := fixed.Integrate(points, 121000)
	if err != nil {
		t.Fatalf("integrate: %v", err)
	}
	// (0+1000)*1000/2 + (1000+0)*1000/2 = 1,000,000.
	if got != 1_000_000 {
		t.Fatalf("integrate = %d, want 1000000", got)
	}
}

func TestBaseEquivalentDivisionByZero(t *testing.T) {
	_, err := fixed.BaseEquivalent(100, 0, domain.LethalityScale)
	assertCode(t, err, domain.CodeDivisionByZero)
}

func TestBaseEquivalentOverflow(t *testing.T) {
	_, err := fixed.BaseEquivalent(math.MaxInt64, 1, 2)
	assertCode(t, err, domain.CodeOverflow)
}

func TestIntegrateNegativeInterval(t *testing.T) {
	points := []fixed.Point{
		{Value: 100, Time: 2000},
		{Value: 100, Time: 1000}, // out of order
	}
	_, err := fixed.Integrate(points, 0)
	assertCode(t, err, domain.CodeNegativeInterval)
}

func TestHalfAwayThresholdBoundary(t *testing.T) {
	// Exactly at the rounding threshold: 1/2 rounds away from zero to 1.
	got, err := fixed.HalfAwayFromZero(1, 2)
	if err != nil {
		t.Fatalf("round: %v", err)
	}
	if got != 1 {
		t.Fatalf("HalfAwayFromZero(1,2) = %d, want 1", got)
	}
}

func TestCalculateEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	planSvc := plan.NewService(st)
	p := fixedPlan()
	if _, err := planSvc.Lock(ctx, plan.LockRequest{OperationID: "op-lock", Plan: p}); err != nil {
		t.Fatalf("lock: %v", err)
	}

	probeSvc := probe.NewService(st)
	registerProbes(t, st, probeSvc, temperatureProbe("probe-1", "batch-a"))
	if err := probeSvc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind", ProbeID: "probe-1", PositionID: "p1", Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40, RangeMin: 121000, RangeMax: 122000,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "op-lease", ResourceID: cycle.ChannelResource("p1"), Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	cycleSvc := cycle.NewService(st)
	lt := domain.LogicalTime(1000)
	for _, ph := range []domain.Phase{domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup, domain.PhaseExposure} {
		if err := cycleSvc.Stage(ctx, cycle.StageRequest{
			OperationID: domain.OperationID("op-stage-" + string(ph)), CycleID: "c1", ValidationID: "v1",
			ExpectedGeneration: 1, Phase: ph, LogicalTime: lt,
		}); err != nil {
			t.Fatalf("stage %s: %v", ph, err)
		}
		lt += 1000
	}
	readings := []int64{121000, 122000, 121000}
	for i, r := range readings {
		if err := cycleSvc.Sample(ctx, cycle.SampleRequest{
			OperationID: domain.OperationID("op-sample-" + string(rune('a'+i))), CycleID: "c1",
			ExpectedGeneration: 1, Token: token,
			Sample: domain.Sample{ProbeID: "probe-1", Sequence: domain.Sequence(i + 1), LogicalTime: lt, Reading: r, Valid: true},
		}); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		lt += 1000
	}

	lethSvc := lethality.NewService(st)
	results, err := lethSvc.Calculate(ctx, lethality.CalculateRequest{OperationID: "op-calc", CycleID: "c1"})
	if err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.PositionID != "p1" {
		t.Fatalf("position = %s, want p1", r.PositionID)
	}
	if r.Accumulated != 243_000_000 {
		t.Fatalf("accumulated = %d, want 243000000", r.Accumulated)
	}
	if r.AlgorithmVersion != lethality.AlgorithmVersion {
		t.Fatalf("algorithm version = %s", r.AlgorithmVersion)
	}
	if r.Lethality <= 0 {
		t.Fatalf("lethality must be positive, got %d", r.Lethality)
	}
}
