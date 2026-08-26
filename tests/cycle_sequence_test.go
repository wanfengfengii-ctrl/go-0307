package tests

import (
	"context"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

var allPhases = []domain.Phase{
	domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup,
	domain.PhaseExposure, domain.PhaseExhaust, domain.PhaseCooling,
}

// stageAll advances a cycle through the six stages in order.
func stageAll(t *testing.T, ctx context.Context, svc *cycle.Service, cycleID domain.CycleID, validationID domain.ValidationID, generation domain.Generation) {
	t.Helper()
	lt := domain.LogicalTime(1000)
	for i, ph := range allPhases {
		err := svc.Stage(ctx, cycle.StageRequest{
			OperationID:        domain.OperationID("op-stage-" + string(rune('a'+i))),
			CycleID:            cycleID,
			ValidationID:       validationID,
			ExpectedGeneration: generation,
			Phase:              ph,
			LogicalTime:        lt,
		})
		if err != nil {
			t.Fatalf("stage %s: %v", ph, err)
		}
		lt += 1000
	}
}

// exposeTo advances the cycle to (and including) exposure and returns the next
// logical time and a bound probe's channel lease token.
func exposeTo(t *testing.T, ctx context.Context, st *store.Store, svc *cycle.Service, cycleID domain.CycleID) (domain.LogicalTime, domain.TokenID) {
	t.Helper()
	probeSvc := probe.NewService(st)
	registerProbes(t, st, probeSvc, temperatureProbe("probe-1", "batch-a"))
	if err := probeSvc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind-s",
		ProbeID:     "probe-1",
		PositionID:  "p1",
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1 << 40,
		RangeMin:    121000,
		RangeMax:    122000,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "op-lease-s",
		ResourceID:  cycle.ChannelResource("p1"),
		Generation:  1,
		ValidFrom:   0,
		ValidUntil:  1 << 40,
	})
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	lt := domain.LogicalTime(1000)
	for _, ph := range []domain.Phase{domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup, domain.PhaseExposure} {
		if err := svc.Stage(ctx, cycle.StageRequest{
			OperationID:        domain.OperationID("op-stage-" + string(ph)),
			CycleID:            cycleID,
			ValidationID:       "v1",
			ExpectedGeneration: 1,
			Phase:              ph,
			LogicalTime:        lt,
		}); err != nil {
			t.Fatalf("stage %s: %v", ph, err)
		}
		lt += 1000
	}
	return lt, token
}

func TestSixStageClosure(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)

	stageAll(t, ctx, svc, "c1", "v1", 1)

	// A seventh stage is rejected because the cycle is complete.
	err := svc.Stage(ctx, cycle.StageRequest{
		OperationID:        "op-stage-extra",
		CycleID:            "c1",
		ValidationID:       "v1",
		ExpectedGeneration: 1,
		Phase:              domain.PhaseCooling,
		LogicalTime:        7000,
	})
	assertCode(t, err, domain.CodeInvalidState)
}

func TestSkipStageRejected(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)

	// Preheat then jump to heatup (skipping replacement).
	if err := svc.Stage(ctx, cycle.StageRequest{
		OperationID: "op-preheat", CycleID: "c2", ValidationID: "v1",
		ExpectedGeneration: 1, Phase: domain.PhasePreheat, LogicalTime: 1000,
	}); err != nil {
		t.Fatalf("preheat: %v", err)
	}
	err := svc.Stage(ctx, cycle.StageRequest{
		OperationID: "op-skip", CycleID: "c2", ValidationID: "v1",
		ExpectedGeneration: 1, Phase: domain.PhaseHeatup, LogicalTime: 2000,
	})
	assertCode(t, err, domain.CodeInvalidPhase)
}

func TestSampleSequenceGap(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)
	lt, token := exposeTo(t, ctx, st, svc, "c3")

	// First sample seq 1 succeeds.
	if err := svc.Sample(ctx, cycle.SampleRequest{
		OperationID: "op-s1", CycleID: "c3", ExpectedGeneration: 1, Token: token,
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: lt, Reading: 121000, Valid: true},
	}); err != nil {
		t.Fatalf("sample 1: %v", err)
	}
	// Jumping to seq 3 leaves a gap.
	err := svc.Sample(ctx, cycle.SampleRequest{
		OperationID: "op-s2", CycleID: "c3", ExpectedGeneration: 1, Token: token,
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 3, LogicalTime: lt + 1000, Reading: 121100, Valid: true},
	})
	assertCode(t, err, domain.CodeSequenceGap)
}

func TestSampleDuplicateSeqConflict(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)
	lt, token := exposeTo(t, ctx, st, svc, "c4")

	first := cycle.SampleRequest{
		OperationID: "op-dup-1", CycleID: "c4", ExpectedGeneration: 1, Token: token,
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: lt, Reading: 121000, Valid: true},
	}
	if err := svc.Sample(ctx, first); err != nil {
		t.Fatalf("sample: %v", err)
	}
	// Same sequence with a different reading is a duplicate-key conflict.
	err := svc.Sample(ctx, cycle.SampleRequest{
		OperationID: "op-dup-2", CycleID: "c4", ExpectedGeneration: 1, Token: token,
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: lt + 1, Reading: 999999, Valid: true},
	})
	assertCode(t, err, domain.CodeDuplicateKey)
}

func TestSampleExpiredToken(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)
	lt, _ := exposeTo(t, ctx, st, svc, "c5")

	// Use a token that has already expired at the sample's logical time.
	err := svc.Sample(ctx, cycle.SampleRequest{
		OperationID: "op-exp", CycleID: "c5", ExpectedGeneration: 1, Token: "bogus-token",
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: lt, Reading: 121000, Valid: true},
	})
	assertCode(t, err, domain.CodeLeaseExpired)
}

func TestStaleGenerationSampleAuditOnly(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := cycle.NewService(st)
	_, token := exposeTo(t, ctx, st, svc, "c6")

	// A late sample for generation 0 must only enter the audit timeline.
	err := svc.Sample(ctx, cycle.SampleRequest{
		OperationID: "op-stale", CycleID: "c6", ExpectedGeneration: 0, Token: token,
		Sample: domain.Sample{ProbeID: "probe-1", Sequence: 1, LogicalTime: 1, Reading: 121000, Valid: true},
	})
	if err != nil {
		t.Fatalf("stale sample: %v", err)
	}

	events, err := svc.Audit(ctx, "c6")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Audit {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an audit event for the stale sample")
	}
}
