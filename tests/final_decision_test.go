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

type cycleEnv struct {
	st        *store.Store
	probeSvc  *probe.Service
	cycleSvc  *cycle.Service
	lethSvc   *lethality.Service
	token     domain.TokenID
	threshold int64
}

// setupReadyCycle builds a cycle with a complete timeline, bound probe, lease,
// samples, calculation, a negative indicator and two approving reviews.
func setupReadyCycle(t *testing.T, ctx context.Context, threshold int64) *cycleEnv {
	t.Helper()
	st := newStore(t)
	env := &cycleEnv{st: st, threshold: threshold}

	planSvc := plan.NewService(st)
	p := fixedPlan()
	p.LethalityThreshold = threshold
	if _, err := planSvc.Lock(ctx, plan.LockRequest{OperationID: "op-lock", Plan: p}); err != nil {
		t.Fatalf("lock: %v", err)
	}

	env.probeSvc = probe.NewService(st)
	registerProbes(t, st, env.probeSvc, temperatureProbe("probe-1", "batch-a"))
	if err := env.probeSvc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind", ProbeID: "probe-1", PositionID: "p1", Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40, RangeMin: 121000, RangeMax: 122000,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	token, err := env.probeSvc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "op-lease", ResourceID: cycle.ChannelResource("p1"), Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	env.token = token

	env.cycleSvc = cycle.NewService(st)
	lt := domain.LogicalTime(1000)
	stage := func(ph domain.Phase) {
		if err := env.cycleSvc.Stage(ctx, cycle.StageRequest{
			OperationID: domain.OperationID("op-stage-" + string(ph)), CycleID: "c1", ValidationID: "v1",
			ExpectedGeneration: 1, Phase: ph, LogicalTime: lt,
		}); err != nil {
			t.Fatalf("stage %s: %v", ph, err)
		}
		lt += 1000
	}
	for _, ph := range []domain.Phase{domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup, domain.PhaseExposure} {
		stage(ph)
	}
	for i, r := range []int64{121000, 122000, 121000} {
		if err := env.cycleSvc.Sample(ctx, cycle.SampleRequest{
			OperationID: domain.OperationID("op-sample-" + string(rune('a'+i))), CycleID: "c1",
			ExpectedGeneration: 1, Token: env.token,
			Sample: domain.Sample{ProbeID: "probe-1", Sequence: domain.Sequence(i + 1), LogicalTime: lt, Reading: r, Valid: true},
		}); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		lt += 1000
	}
	stage(domain.PhaseExhaust)
	stage(domain.PhaseCooling)

	env.lethSvc = lethality.NewService(st)
	if _, err := env.lethSvc.Calculate(ctx, lethality.CalculateRequest{OperationID: "op-calc", CycleID: "c1"}); err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if err := env.lethSvc.RecordIndicator(ctx, "c1", 1, domain.BiologicalIndicator{
		ID: "bi-1", PositionID: "p1", Result: domain.IndicatorNegative, Evidence: "cultured",
	}); err != nil {
		t.Fatalf("indicator: %v", err)
	}
	for _, reviewer := range []string{"reviewer-a", "reviewer-b"} {
		if err := env.lethSvc.Review(ctx, "c1", domain.Review{
			ReviewerID: reviewer, Qualified: true, Conclusion: domain.ReviewApprove,
		}); err != nil {
			t.Fatalf("review %s: %v", reviewer, err)
		}
	}
	return env
}

func TestFinalDecisionRelease(t *testing.T) {
	ctx := context.Background()
	env := setupReadyCycle(t, ctx, 10000)

	err := env.lethSvc.Decide(ctx, "c1", domain.FinalDecision{
		Decision: domain.DecisionRelease, Credential: "cred-1", OperationID: "op-final",
	})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	got, err := env.lethSvc.Final(ctx, "c1")
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if got.Decision != domain.DecisionRelease {
		t.Fatalf("decision = %s, want release", got.Decision)
	}
}

func TestFinalDecisionInsufficientLethality(t *testing.T) {
	ctx := context.Background()
	// A threshold far above the computed lethality is rejected.
	env := setupReadyCycle(t, ctx, 1_000_000_000)

	err := env.lethSvc.Decide(ctx, "c1", domain.FinalDecision{
		Decision: domain.DecisionRelease, Credential: "cred-1", OperationID: "op-final",
	})
	assertCode(t, err, domain.CodeInsufficientLethality)
}

func TestFinalDecisionInvalidIndicator(t *testing.T) {
	ctx := context.Background()
	env := setupReadyCycle(t, ctx, 10000)

	// A positive biological indicator invalidates release.
	if err := env.lethSvc.RecordIndicator(ctx, "c1", 1, domain.BiologicalIndicator{
		ID: "bi-2", PositionID: "p2", Result: domain.IndicatorPositive, Evidence: "growth",
	}); err != nil {
		t.Fatalf("indicator: %v", err)
	}
	err := env.lethSvc.Decide(ctx, "c1", domain.FinalDecision{
		Decision: domain.DecisionRelease, Credential: "cred-1", OperationID: "op-final",
	})
	assertCode(t, err, domain.CodeInvalidIndicator)
}

func TestFinalDecisionRetestOpen(t *testing.T) {
	ctx := context.Background()
	env := setupReadyCycle(t, ctx, 10000)

	if _, err := env.lethSvc.OpenRetest(ctx, lethality.RetestRequest{
		OperationID: "op-retest", CycleID: "c1", Sources: []string{"p1"},
	}); err != nil {
		t.Fatalf("open retest: %v", err)
	}
	err := env.lethSvc.Decide(ctx, "c1", domain.FinalDecision{
		Decision: domain.DecisionRelease, Credential: "cred-1", OperationID: "op-final",
	})
	assertCode(t, err, domain.CodeRetestOpen)
}

func TestFinalDecisionReviewerConflict(t *testing.T) {
	ctx := context.Background()
	env := setupReadyCycle(t, ctx, 10000)

	// A single reviewer reviewing twice must be rejected.
	err := env.lethSvc.Review(ctx, "c1", domain.Review{
		ReviewerID: "reviewer-a", Qualified: true, Conclusion: domain.ReviewApprove,
	})
	assertCode(t, err, domain.CodeReviewerConflict)
}

func TestFinalDecisionConcurrentSingleWinner(t *testing.T) {
	ctx := context.Background()
	env := setupReadyCycle(t, ctx, 10000)

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = env.lethSvc.Decide(ctx, "c1", domain.FinalDecision{
				Decision:    domain.DecisionRelease,
				Credential:  "cred-" + string(rune('a'+i)),
				OperationID: domain.OperationID("op-final-" + string(rune('a'+i))),
			})
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			assertCode(t, err, domain.CodeFinalConflict)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one final winner, got %d", successes)
	}
}
