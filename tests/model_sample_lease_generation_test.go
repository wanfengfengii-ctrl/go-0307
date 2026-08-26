package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_SampleLeaseGeneration(t *testing.T) {
	tests := []struct {
		name               string
		leaseGeneration    domain.Generation
		expectedGeneration domain.Generation
		wantStatus         int
		wantCode           domain.ErrorCode
		wantSamples        int
		wantAudit          bool
	}{
		{
			name:               "old generation lease cannot authorize current evidence",
			leaseGeneration:    0,
			expectedGeneration: 1,
			wantStatus:         http.StatusConflict,
			wantCode:           domain.CodeLeaseExpired,
			wantSamples:        0,
		},
		{
			name:               "current generation lease remains accepted",
			leaseGeneration:    1,
			expectedGeneration: 1,
			wantStatus:         http.StatusOK,
			wantSamples:        1,
		},
		{
			name:               "old generation sample remains audit only",
			leaseGeneration:    0,
			expectedGeneration: 0,
			wantStatus:         http.StatusOK,
			wantSamples:        0,
			wantAudit:          true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t)
			cycleSvc := cycle.NewService(st)
			probeSvc := probe.NewService(st)

			if err := probeSvc.Register(ctx, temperatureProbe("probe-1", "batch-a")); err != nil {
				t.Fatalf("register probe: %v", err)
			}
			if err := probeSvc.Bind(ctx, probe.BindRequest{
				OperationID: "op-bind-model",
				ProbeID:     "probe-1",
				PositionID:  "p1",
				Generation:  1,
				ValidFrom:   0,
				ValidUntil:  10000,
				RangeMin:    121000,
				RangeMax:    122000,
			}); err != nil {
				t.Fatalf("bind probe: %v", err)
			}
			token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
				OperationID: domain.OperationID("op-lease-model-" + string(rune('a'+i))),
				ResourceID:  cycle.ChannelResource("p1"),
				Generation:  tt.leaseGeneration,
				ValidFrom:   0,
				ValidUntil:  10000,
			})
			if err != nil {
				t.Fatalf("acquire lease: %v", err)
			}

			for j, phase := range []domain.Phase{
				domain.PhasePreheat,
				domain.PhaseReplacement,
				domain.PhaseHeatup,
				domain.PhaseExposure,
			} {
				if err := cycleSvc.Stage(ctx, cycle.StageRequest{
					OperationID:        domain.OperationID("op-stage-model-" + string(rune('a'+j))),
					CycleID:            "cycle-model",
					ValidationID:       "v1",
					ExpectedGeneration: 1,
					Phase:              phase,
					LogicalTime:        domain.LogicalTime((j + 1) * 1000),
				}); err != nil {
					t.Fatalf("stage %s: %v", phase, err)
				}
			}

			body, err := json.Marshal(map[string]any{
				"operation_id":        "op-sample-model",
				"expected_generation": tt.expectedGeneration,
				"token":               token,
				"sample": domain.Sample{
					ProbeID:       "probe-1",
					Sequence:      1,
					LogicalTime:   5000,
					Reading:       121000,
					DeviceReceipt: "receipt-1",
					Valid:         true,
				},
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/cycles/cycle-model/samples", bytes.NewReader(body))
			httpapi.NewServer(st).ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("POST sample status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantCode != "" {
				var response domain.Error
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if response.Code != tt.wantCode {
					t.Fatalf("POST sample error code = %q, want %q", response.Code, tt.wantCode)
				}
			}

			var samples []domain.Sample
			if err := st.Read(ctx, func(tx *store.Tx) error {
				var err error
				samples, err = tx.ListSamplesByProbe(ctx, "cycle-model", 1, "probe-1")
				return err
			}); err != nil {
				t.Fatalf("list current-generation samples: %v", err)
			}
			if len(samples) != tt.wantSamples {
				t.Fatalf("current-generation samples = %d, want %d", len(samples), tt.wantSamples)
			}

			events, err := cycleSvc.Audit(ctx, "cycle-model")
			if err != nil {
				t.Fatalf("read audit: %v", err)
			}
			foundAudit := false
			for _, event := range events {
				foundAudit = foundAudit || event.Audit
			}
			if foundAudit != tt.wantAudit {
				t.Fatalf("audit event present = %v, want %v", foundAudit, tt.wantAudit)
			}
		})
	}
}
