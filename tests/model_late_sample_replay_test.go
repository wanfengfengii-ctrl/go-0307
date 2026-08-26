package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/lethality"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_LateSampleReplayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	cycleID := domain.CycleID("cycle-late-replay")

	planSvc := plan.NewService(st)
	if _, err := planSvc.Lock(ctx, plan.LockRequest{OperationID: "op-lock-late-replay", Plan: fixedPlan()}); err != nil {
		t.Fatalf("lock plan: %v", err)
	}

	probeSvc := probe.NewService(st)
	registerProbes(t, st, probeSvc, temperatureProbe("probe-1", "batch-late-replay"))
	if err := probeSvc.Bind(ctx, probe.BindRequest{
		OperationID: "op-bind-late-replay", ProbeID: "probe-1", PositionID: "p1", Generation: 2,
		ValidFrom: 0, ValidUntil: 1 << 40, RangeMin: 121000, RangeMax: 122000,
	}); err != nil {
		t.Fatalf("bind current generation: %v", err)
	}
	token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "op-lease-late-replay", ResourceID: cycle.ChannelResource("p1"), Generation: 2,
		ValidFrom: 0, ValidUntil: 1 << 40,
	})
	if err != nil {
		t.Fatalf("acquire current-generation lease: %v", err)
	}

	cycleSvc := cycle.NewService(st)
	for i, phase := range []domain.Phase{
		domain.PhasePreheat, domain.PhaseReplacement, domain.PhaseHeatup, domain.PhaseExposure,
	} {
		if err := cycleSvc.Stage(ctx, cycle.StageRequest{
			OperationID: domain.OperationID("op-current-stage-" + string(phase)),
			CycleID:     cycleID, ValidationID: "v1", ExpectedGeneration: 2,
			Phase: phase, LogicalTime: domain.LogicalTime((i + 1) * 1000),
		}); err != nil {
			t.Fatalf("stage %s: %v", phase, err)
		}
	}

	for i, reading := range []int64{121000, 122000, 121000} {
		if err := cycleSvc.Sample(ctx, cycle.SampleRequest{
			OperationID: domain.OperationID("op-current-sample-" + string(rune('a'+i))),
			CycleID:     cycleID, ExpectedGeneration: 2, Token: token,
			Sample: domain.Sample{
				ProbeID: "probe-1", Sequence: domain.Sequence(i + 1),
				LogicalTime: domain.LogicalTime((i + 5) * 1000), Reading: reading, Valid: true,
			},
		}); err != nil {
			t.Fatalf("current-generation sample %d: %v", i+1, err)
		}
	}

	lethalitySvc := lethality.NewService(st)
	baselineResults, err := lethalitySvc.Calculate(ctx, lethality.CalculateRequest{
		OperationID: "op-calculate-before-late", CycleID: cycleID,
	})
	if err != nil {
		t.Fatalf("baseline calculation: %v", err)
	}
	var baselineSamples []domain.Sample
	if err := st.Read(ctx, func(tx *store.Tx) error {
		var readErr error
		baselineSamples, readErr = tx.ListSamplesByProbe(ctx, cycleID, 2, "probe-1")
		return readErr
	}); err != nil {
		t.Fatalf("baseline samples: %v", err)
	}

	handler := httpapi.NewServer(st)
	postSample := func(body any) *httptest.ResponseRecorder {
		payload, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			t.Fatalf("marshal sample request: %v", marshalErr)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/cycles/"+string(cycleID)+"/samples", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		return rec
	}
	getAudit := func() []domain.CycleEvent {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cycles/"+string(cycleID)+"/audit", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET audit status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var events []domain.CycleEvent
		if decodeErr := json.Unmarshal(rec.Body.Bytes(), &events); decodeErr != nil {
			t.Fatalf("decode audit: %v", decodeErr)
		}
		return events
	}

	lateA := map[string]any{
		"operation_id": "op-late-a", "expected_generation": 1, "token": "old-gateway-token",
		"sample": domain.Sample{ProbeID: "probe-1", Sequence: 41, LogicalTime: 3500, Reading: 119500, DeviceReceipt: "late-a", Valid: true},
	}
	lateB := map[string]any{
		"operation_id": "op-late-b", "expected_generation": 1, "token": "old-gateway-token",
		"sample": domain.Sample{ProbeID: "probe-1", Sequence: 42, LogicalTime: 3600, Reading: 119600, DeviceReceipt: "late-b", Valid: true},
	}

	var firstResponse []byte
	cases := []struct {
		name           string
		body           any
		wantCursor     domain.Sequence
		wantAuditOps   map[domain.OperationID]int
		compareToFirst bool
	}{
		{name: "first late sample is audited", body: lateA, wantCursor: 5, wantAuditOps: map[domain.OperationID]int{"op-late-a": 1}},
		{name: "same operation replay returns first result", body: lateA, wantCursor: 5, wantAuditOps: map[domain.OperationID]int{"op-late-a": 1}, compareToFirst: true},
		{name: "different operation is an independent late sample", body: lateB, wantCursor: 6, wantAuditOps: map[domain.OperationID]int{"op-late-a": 1, "op-late-b": 1}},
		{name: "earlier operation remains replayable", body: lateA, wantCursor: 6, wantAuditOps: map[domain.OperationID]int{"op-late-a": 1, "op-late-b": 1}, compareToFirst: true},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postSample(tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST sample status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
			if i == 0 {
				firstResponse = append([]byte(nil), rec.Body.Bytes()...)
			}
			if tc.compareToFirst && !bytes.Equal(rec.Body.Bytes(), firstResponse) {
				t.Fatalf("replay response = %q, want first response %q", rec.Body.Bytes(), firstResponse)
			}

			events := getAudit()
			gotAuditOps := make(map[domain.OperationID]int)
			for _, event := range events {
				if event.Audit {
					gotAuditOps[event.OperationID]++
				}
			}
			if !reflect.DeepEqual(gotAuditOps, tc.wantAuditOps) {
				t.Fatalf("audit operations = %#v, want %#v", gotAuditOps, tc.wantAuditOps)
			}

			var snap domain.CycleSnapshot
			var currentSamples []domain.Sample
			if readErr := st.Read(ctx, func(tx *store.Tx) error {
				var txErr error
				snap, txErr = tx.GetSnapshot(ctx, cycleID)
				if txErr != nil {
					return txErr
				}
				currentSamples, txErr = tx.ListSamplesByProbe(ctx, cycleID, 2, "probe-1")
				return txErr
			}); readErr != nil {
				t.Fatalf("read current projection: %v", readErr)
			}
			if snap.Generation != 2 || snap.Cursor != tc.wantCursor {
				t.Fatalf("snapshot generation/cursor = %d/%d, want 2/%d", snap.Generation, snap.Cursor, tc.wantCursor)
			}
			if !reflect.DeepEqual(currentSamples, baselineSamples) {
				t.Fatalf("current-generation samples changed: got %#v, want %#v", currentSamples, baselineSamples)
			}
			gotResults, resultsErr := lethalitySvc.Results(ctx, cycleID)
			if resultsErr != nil {
				t.Fatalf("current results: %v", resultsErr)
			}
			if !reflect.DeepEqual(gotResults, baselineResults) {
				t.Fatalf("current-generation results changed: got %#v, want %#v", gotResults, baselineResults)
			}
		})
	}
}
