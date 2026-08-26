package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/plan"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_RetestGenerationBecomesCurrent(t *testing.T) {
	type environment struct {
		handler http.Handler
		store   *store.Store
	}

	post := func(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s request: %v", path, err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	get := func(handler http.Handler, path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	setup := func(t *testing.T, throughExposure bool) environment {
		t.Helper()
		ctx := context.Background()
		st := newStore(t)
		if _, err := plan.NewService(st).Lock(ctx, plan.LockRequest{OperationID: "model-lock", Plan: fixedPlan()}); err != nil {
			t.Fatalf("lock plan: %v", err)
		}

		probeService := probe.NewService(st)
		batches := []string{"batch-a", "batch-a", "batch-b", "batch-b"}
		for i, batch := range batches {
			probeID := domain.ProbeID(fmt.Sprintf("probe-%d", i+1))
			if err := probeService.Register(ctx, temperatureProbe(probeID, batch)); err != nil {
				t.Fatalf("register %s: %v", probeID, err)
			}
			if err := probeService.Bind(ctx, probe.BindRequest{
				OperationID: domain.OperationID(fmt.Sprintf("model-bind-%d", i+1)),
				ProbeID:     probeID, PositionID: domain.PositionID(fmt.Sprintf("p%d", i+1)),
				Generation: 1, ValidFrom: 0, ValidUntil: 100,
				RangeMin: 121000, RangeMax: 122000,
			}); err != nil {
				t.Fatalf("bind %s: %v", probeID, err)
			}
		}

		phases := []domain.Phase{domain.PhasePreheat}
		if throughExposure {
			phases = []domain.Phase{
				domain.PhasePreheat, domain.PhaseReplacement,
				domain.PhaseHeatup, domain.PhaseExposure,
			}
		}
		cycleService := cycle.NewService(st)
		for i, phase := range phases {
			if err := cycleService.Stage(ctx, cycle.StageRequest{
				OperationID: domain.OperationID(fmt.Sprintf("model-initial-stage-%d", i)),
				CycleID:     "model-cycle", ValidationID: "v1", ExpectedGeneration: 1,
				Phase: phase, LogicalTime: domain.LogicalTime((i + 1) * 10),
			}); err != nil {
				t.Fatalf("initial stage %s: %v", phase, err)
			}
		}
		return environment{handler: httpapi.NewServer(st), store: st}
	}

	openRetest := func(t *testing.T, handler http.Handler, operation string, sources []string) domain.Generation {
		t.Helper()
		recorder := post(t, handler, "/api/v1/cycles/model-cycle/deviations", map[string]any{
			"OperationID": operation,
			"Sources":     sources,
		})
		if recorder.Code != http.StatusOK {
			t.Fatalf("open retest status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Generation domain.Generation `json:"retest_generation"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode retest response: %v", err)
		}
		if response.Generation != 2 {
			t.Fatalf("retest_generation = %d, want 2", response.Generation)
		}
		return response.Generation
	}

	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "returned generation accepts the next retest stage",
			run: func(t *testing.T) {
				env := setup(t, false)
				generation := openRetest(t, env.handler, "model-retest-stage", []string{"p1"})
				recorder := post(t, env.handler, "/api/v1/cycles/model-cycle/stages", map[string]any{
					"OperationID": "model-retest-preheat", "ValidationID": "v1",
					"ExpectedGeneration": generation, "Phase": domain.PhasePreheat, "LogicalTime": 110,
				})
				if recorder.Code != http.StatusOK {
					t.Fatalf("retest stage status = %d, body = %s", recorder.Code, recorder.Body.String())
				}
			},
		},
		{
			name: "returned generation accepts retest samples",
			run: func(t *testing.T) {
				env := setup(t, false)
				generation := openRetest(t, env.handler, "model-retest-sample", []string{"p1"})
				probeService := probe.NewService(env.store)
				if err := probeService.Bind(context.Background(), probe.BindRequest{
					OperationID: "model-bind-retest", ProbeID: "probe-1", PositionID: "p1",
					Generation: generation, ValidFrom: 100, ValidUntil: 1000,
					RangeMin: 121000, RangeMax: 122000,
				}); err != nil {
					t.Fatalf("bind retest probe: %v", err)
				}
				token, err := probeService.Acquire(context.Background(), probe.LeaseRequest{
					OperationID: "model-lease-retest", ResourceID: cycle.ChannelResource("p1"),
					Generation: generation, ValidFrom: 100, ValidUntil: 1000,
				})
				if err != nil {
					t.Fatalf("acquire retest lease: %v", err)
				}
				for i, phase := range []domain.Phase{
					domain.PhasePreheat, domain.PhaseReplacement,
					domain.PhaseHeatup, domain.PhaseExposure,
				} {
					recorder := post(t, env.handler, "/api/v1/cycles/model-cycle/stages", map[string]any{
						"OperationID": fmt.Sprintf("model-retest-stage-%d", i), "ValidationID": "v1",
						"ExpectedGeneration": generation, "Phase": phase, "LogicalTime": 110 + i*10,
					})
					if recorder.Code != http.StatusOK {
						t.Fatalf("retest stage %s status = %d, body = %s", phase, recorder.Code, recorder.Body.String())
					}
				}
				recorder := post(t, env.handler, "/api/v1/cycles/model-cycle/samples", map[string]any{
					"operation_id": "model-retest-sample-1", "expected_generation": generation, "token": token,
					"sample": map[string]any{
						"probe_id": "probe-1", "sequence": 1, "logical_time": 150,
						"reading": 121000, "device_receipt": "retest-receipt", "valid": true,
					},
				})
				if recorder.Code != http.StatusOK {
					t.Fatalf("retest sample status = %d, body = %s", recorder.Code, recorder.Body.String())
				}
			},
		},
		{
			name: "same propagation reuses sorted deduplicated generation",
			run: func(t *testing.T) {
				env := setup(t, false)
				generation := openRetest(t, env.handler, "model-retest-dedup-1", []string{"p2", "p1", "p1"})
				if got := openRetest(t, env.handler, "model-retest-dedup-2", []string{"p1"}); got != generation {
					t.Fatalf("replayed propagation generation = %d, want %d", got, generation)
				}
				recorder := get(env.handler, fmt.Sprintf("/api/v1/cycles/model-cycle/retests?generation=%d", generation))
				if recorder.Code != http.StatusOK {
					t.Fatalf("list retest members status = %d, body = %s", recorder.Code, recorder.Body.String())
				}
				var members []domain.RetestMember
				if err := json.Unmarshal(recorder.Body.Bytes(), &members); err != nil {
					t.Fatalf("decode retest members: %v", err)
				}
				if len(members) != 2 || members[0].Position != "p1" || members[1].Position != "p2" {
					t.Fatalf("members = %+v, want unique sorted positions p1, p2", members)
				}
			},
		},
		{
			name: "old generation sample is audit only",
			run: func(t *testing.T) {
				env := setup(t, true)
				probeService := probe.NewService(env.store)
				token, err := probeService.Acquire(context.Background(), probe.LeaseRequest{
					OperationID: "model-lease-old", ResourceID: cycle.ChannelResource("p1"),
					Generation: 1, ValidFrom: 0, ValidUntil: 100,
				})
				if err != nil {
					t.Fatalf("acquire old-generation lease: %v", err)
				}
				openRetest(t, env.handler, "model-retest-audit", []string{"p1"})
				recorder := post(t, env.handler, "/api/v1/cycles/model-cycle/samples", map[string]any{
					"operation_id": "model-late-sample", "expected_generation": 1, "token": token,
					"sample": map[string]any{
						"probe_id": "probe-1", "sequence": 1, "logical_time": 50,
						"reading": 121000, "device_receipt": "late-receipt", "valid": true,
					},
				})
				if recorder.Code != http.StatusOK {
					t.Fatalf("late sample status = %d, body = %s", recorder.Code, recorder.Body.String())
				}
				recorder = get(env.handler, "/api/v1/cycles/model-cycle/audit")
				if recorder.Code != http.StatusOK {
					t.Fatalf("audit status = %d, body = %s", recorder.Code, recorder.Body.String())
				}
				var events []domain.CycleEvent
				if err := json.Unmarshal(recorder.Body.Bytes(), &events); err != nil {
					t.Fatalf("decode audit: %v", err)
				}
				if len(events) == 0 || !events[len(events)-1].Audit || events[len(events)-1].Generation != 2 {
					t.Fatalf("last event = %+v, want generation-2 audit event", events[len(events)-1])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
