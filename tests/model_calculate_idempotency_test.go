package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
)

func TestModel_CalculateIdempotentRetry(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	p := fixedPlan()
	if _, err := plan.NewService(st).Lock(ctx, plan.LockRequest{OperationID: "model-lock", Plan: p}); err != nil {
		t.Fatalf("lock plan: %v", err)
	}

	probeSvc := probe.NewService(st)
	registerProbes(t, st, probeSvc, temperatureProbe("probe-1", "model-batch"))
	if err := probeSvc.Bind(ctx, probe.BindRequest{
		OperationID: "model-bind", ProbeID: "probe-1", PositionID: "p1", Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40, RangeMin: 121000, RangeMax: 130000,
	}); err != nil {
		t.Fatalf("bind probe: %v", err)
	}
	token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
		OperationID: "model-lease", ResourceID: cycle.ChannelResource("p1"), Generation: 1,
		ValidFrom: 0, ValidUntil: 1 << 40,
	})
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}

	cycleSvc := cycle.NewService(st)
	prepareCycle := func(cycleID domain.CycleID, middleReading int64) {
		t.Helper()
		logicalTime := domain.LogicalTime(1000)
		for i, phase := range []domain.Phase{
			domain.PhasePreheat,
			domain.PhaseReplacement,
			domain.PhaseHeatup,
			domain.PhaseExposure,
		} {
			err := cycleSvc.Stage(ctx, cycle.StageRequest{
				OperationID:        domain.OperationID(fmt.Sprintf("model-%s-stage-%d", cycleID, i)),
				CycleID:            cycleID,
				ValidationID:       "v1",
				ExpectedGeneration: 1,
				Phase:              phase,
				LogicalTime:        logicalTime,
			})
			if err != nil {
				t.Fatalf("stage %s for %s: %v", phase, cycleID, err)
			}
			logicalTime += 1000
		}
		for i, reading := range []int64{121000, middleReading, 121000} {
			err := cycleSvc.Sample(ctx, cycle.SampleRequest{
				OperationID:        domain.OperationID(fmt.Sprintf("model-%s-sample-%d", cycleID, i)),
				CycleID:            cycleID,
				ExpectedGeneration: 1,
				Token:              token,
				Sample: domain.Sample{
					ProbeID: "probe-1", Sequence: domain.Sequence(i + 1),
					LogicalTime: logicalTime, Reading: reading, Valid: true,
				},
			})
			if err != nil {
				t.Fatalf("sample %d for %s: %v", i, cycleID, err)
			}
			logicalTime += 1000
		}
		for i, phase := range []domain.Phase{domain.PhaseExhaust, domain.PhaseCooling} {
			err := cycleSvc.Stage(ctx, cycle.StageRequest{
				OperationID:        domain.OperationID(fmt.Sprintf("model-%s-close-%d", cycleID, i)),
				CycleID:            cycleID,
				ValidationID:       "v1",
				ExpectedGeneration: 1,
				Phase:              phase,
				LogicalTime:        logicalTime,
			})
			if err != nil {
				t.Fatalf("stage %s for %s: %v", phase, cycleID, err)
			}
			logicalTime += 1000
		}
	}
	prepareCycle("c1", 122000)
	prepareCycle("c2", 124000)

	handler := httpapi.NewServer(st)
	var first []domain.CalculationResult
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		check      func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:   "first calculation",
			method: http.MethodPost, path: "/api/v1/cycles/c1/calculate",
			body: `{"OperationID":"model-calculate"}`, wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
					t.Fatalf("decode first calculation: %v", err)
				}
				if len(first) != 1 {
					t.Fatalf("first calculation returned %d results, want 1", len(first))
				}
			},
		},
		{
			name:   "normalized retry replays first result",
			method: http.MethodPost, path: "/api/v1/cycles/c1/calculate",
			body: "{\n  \"operationid\" : \"model-calculate\"\n}", wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var replay []domain.CalculationResult
				if err := json.Unmarshal(rec.Body.Bytes(), &replay); err != nil {
					t.Fatalf("decode replay: %v", err)
				}
				if !reflect.DeepEqual(replay, first) {
					t.Fatalf("replay = %#v, want first result %#v", replay, first)
				}
			},
		},
		{
			name:   "results remain singular",
			method: http.MethodGet, path: "/api/v1/cycles/c1/results",
			wantStatus: http.StatusOK,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var stored []domain.CalculationResult
				if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
					t.Fatalf("decode stored results: %v", err)
				}
				if len(stored) != 1 || !reflect.DeepEqual(stored, first) {
					t.Fatalf("stored results after retry = %#v, want one copy of %#v", stored, first)
				}
			},
		},
		{
			name:   "operation reused for different cycle conflicts",
			method: http.MethodPost, path: "/api/v1/cycles/c2/calculate",
			body: `{"OperationID":"model-calculate"}`, wantStatus: http.StatusConflict,
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var got domain.Error
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode conflict: %v", err)
				}
				if got.Code != domain.CodeIdempotencyConflict {
					t.Fatalf("conflict code = %q, want %q", got.Code, domain.CodeIdempotencyConflict)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			tc.check(t, rec)
		})
	}
}
