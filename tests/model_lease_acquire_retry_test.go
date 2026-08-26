package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/probe"
)

func TestModel_LeaseAcquireRetryContract(t *testing.T) {
	type outcome struct {
		status int
		token  domain.TokenID
		code   domain.ErrorCode
	}
	type testCase struct {
		name       string
		requests   []probe.LeaseRequest
		concurrent bool
		wantStatus int
		wantCode   domain.ErrorCode
		sameToken  bool
	}

	base := probe.LeaseRequest{
		OperationID: "op-acquire-lost-response",
		ResourceID:  "channel:probe-1",
		Generation:  7,
		ValidFrom:   1000,
		ValidUntil:  2000,
	}
	cases := []testCase{
		{
			name:       "identical retry returns original token",
			requests:   []probe.LeaseRequest{base, base},
			wantStatus: http.StatusOK,
			sameToken:  true,
		},
		{
			name: "same operation with changed overlapping interval conflicts",
			requests: []probe.LeaseRequest{base, {
				OperationID: base.OperationID,
				ResourceID:  base.ResourceID,
				Generation:  base.Generation,
				ValidFrom:   1500,
				ValidUntil:  2500,
			}},
			wantStatus: http.StatusConflict,
			wantCode:   domain.CodeIdempotencyConflict,
		},
		{
			name: "different operation with overlapping interval conflicts",
			requests: []probe.LeaseRequest{base, {
				OperationID: "op-acquire-competitor",
				ResourceID:  base.ResourceID,
				Generation:  base.Generation,
				ValidFrom:   base.ValidFrom,
				ValidUntil:  base.ValidUntil,
			}},
			wantStatus: http.StatusConflict,
			wantCode:   domain.CodeLeaseConflict,
		},
		{
			name: "concurrent contenders have one winner",
			requests: []probe.LeaseRequest{{
				OperationID: "op-acquire-racer-a",
				ResourceID:  base.ResourceID,
				Generation:  base.Generation,
				ValidFrom:   base.ValidFrom,
				ValidUntil:  base.ValidUntil,
			}, {
				OperationID: "op-acquire-racer-b",
				ResourceID:  base.ResourceID,
				Generation:  base.Generation,
				ValidFrom:   base.ValidFrom,
				ValidUntil:  base.ValidUntil,
			}},
			concurrent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := httpapi.NewServer(newStore(t))
			acquire := func(req probe.LeaseRequest) outcome {
				body, err := json.Marshal(req)
				if err != nil {
					t.Fatalf("marshal lease request: %v", err)
				}
				recorder := httptest.NewRecorder()
				httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/leases/acquire", bytes.NewReader(body))
				httpReq.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(recorder, httpReq)

				got := outcome{status: recorder.Code}
				if recorder.Code == http.StatusOK {
					var response struct {
						Token domain.TokenID `json:"token"`
					}
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatalf("decode success response: %v", err)
					}
					got.token = response.Token
				} else {
					var response domain.Error
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatalf("decode error response: %v", err)
					}
					got.code = response.Code
				}
				return got
			}

			if tc.concurrent {
				start := make(chan struct{})
				results := make([]outcome, len(tc.requests))
				var wg sync.WaitGroup
				for i, req := range tc.requests {
					wg.Add(1)
					go func(i int, req probe.LeaseRequest) {
						defer wg.Done()
						<-start
						results[i] = acquire(req)
					}(i, req)
				}
				close(start)
				wg.Wait()

				winners, conflicts := 0, 0
				for _, got := range results {
					switch {
					case got.status == http.StatusOK && got.token != "":
						winners++
					case got.status == http.StatusConflict && got.code == domain.CodeLeaseConflict:
						conflicts++
					default:
						t.Fatalf("concurrent outcome = %+v, want a token or LEASE_CONFLICT", got)
					}
				}
				if winners != 1 || conflicts != 1 {
					t.Fatalf("concurrent outcomes: winners=%d conflicts=%d, want 1 each", winners, conflicts)
				}
				return
			}

			first := acquire(tc.requests[0])
			if first.status != http.StatusOK || first.token == "" {
				t.Fatalf("initial acquire = %+v, want status 200 with token", first)
			}
			second := acquire(tc.requests[1])
			if second.status != tc.wantStatus {
				t.Fatalf("second acquire status = %d, want %d", second.status, tc.wantStatus)
			}
			if tc.sameToken && second.token != first.token {
				t.Fatalf("retry token = %q, want original %q", second.token, first.token)
			}
			if tc.wantCode != "" && second.code != tc.wantCode {
				t.Fatalf("second acquire code = %q, want %q", second.code, tc.wantCode)
			}
		})
	}
}
