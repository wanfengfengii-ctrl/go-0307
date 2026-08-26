package tests

import (
	"context"
	"testing"

	"lyophilizer-sterilization-validation/internal/cycle"
	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/probe"
	"lyophilizer-sterilization-validation/internal/store"
)

func TestModel_ProbeBindingIntervalGatesSamples(t *testing.T) {
	tests := []struct {
		name         string
		bindingFrom  domain.LogicalTime
		bindingUntil domain.LogicalTime
		sampleTime   domain.LogicalTime
		wantCode     domain.ErrorCode
		wantSamples  int
	}{
		{
			name:         "expired binding despite active channel lease",
			bindingFrom:  0,
			bindingUntil: 3500,
			sampleTime:   5000,
			wantCode:     domain.CodeLeaseExpired,
		},
		{
			name:         "binding not yet effective despite active channel lease",
			bindingFrom:  6000,
			bindingUntil: 9000,
			sampleTime:   5000,
			wantCode:     domain.CodeLeaseExpired,
		},
		{
			name:         "binding start is inclusive",
			bindingFrom:  5000,
			bindingUntil: 9000,
			sampleTime:   5000,
			wantSamples:  1,
		},
		{
			name:         "binding end is exclusive",
			bindingFrom:  0,
			bindingUntil: 5000,
			sampleTime:   5000,
			wantCode:     domain.CodeLeaseExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			st := newStore(t)
			cycleSvc := cycle.NewService(st)
			probeSvc := probe.NewService(st)

			registerProbes(t, st, probeSvc, temperatureProbe("probe-binding-window", "batch-a"))
			if err := probeSvc.Bind(ctx, probe.BindRequest{
				OperationID: "op-bind-window",
				ProbeID:     "probe-binding-window",
				PositionID:  "p1",
				Generation:  1,
				ValidFrom:   tt.bindingFrom,
				ValidUntil:  tt.bindingUntil,
				RangeMin:    121000,
				RangeMax:    122000,
			}); err != nil {
				t.Fatalf("bind probe: %v", err)
			}

			token, err := probeSvc.Acquire(ctx, probe.LeaseRequest{
				OperationID: "op-lease-window",
				ResourceID:  cycle.ChannelResource("p1"),
				Generation:  1,
				ValidFrom:   0,
				ValidUntil:  10000,
			})
			if err != nil {
				t.Fatalf("acquire channel lease: %v", err)
			}

			for i, phase := range []domain.Phase{
				domain.PhasePreheat,
				domain.PhaseReplacement,
				domain.PhaseHeatup,
				domain.PhaseExposure,
			} {
				if err := cycleSvc.Stage(ctx, cycle.StageRequest{
					OperationID:        domain.OperationID("op-stage-window-" + string(phase)),
					CycleID:            "cycle-binding-window",
					ValidationID:       "validation-binding-window",
					ExpectedGeneration: 1,
					Phase:              phase,
					LogicalTime:        domain.LogicalTime((i + 1) * 1000),
				}); err != nil {
					t.Fatalf("stage %s: %v", phase, err)
				}
			}

			err = cycleSvc.Sample(ctx, cycle.SampleRequest{
				OperationID:        "op-sample-window",
				CycleID:            "cycle-binding-window",
				ExpectedGeneration: 1,
				Token:              token,
				Sample: domain.Sample{
					ProbeID:     "probe-binding-window",
					Sequence:    1,
					LogicalTime: tt.sampleTime,
					Reading:     121000,
					Valid:       true,
				},
			})
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("sample within binding interval: %v", err)
				}
			} else {
				assertCode(t, err, tt.wantCode)
			}

			var samples []domain.Sample
			if err := st.Read(ctx, func(tx *store.Tx) error {
				var readErr error
				samples, readErr = tx.ListSamplesByProbe(ctx, "cycle-binding-window", 1, "probe-binding-window")
				return readErr
			}); err != nil {
				t.Fatalf("list stored samples: %v", err)
			}
			if len(samples) != tt.wantSamples {
				t.Fatalf("stored samples = %d, want %d", len(samples), tt.wantSamples)
			}
		})
	}
}
