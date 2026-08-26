package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/probe"
)

func TestModel_LeaseRenewalPreservesGrantedInterval(t *testing.T) {
	post := func(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s request: %v", path, err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}
	mustAcquire := func(t *testing.T, h http.Handler, operation domain.OperationID, from, until domain.LogicalTime) domain.TokenID {
		t.Helper()
		rec := post(t, h, "/api/v1/leases/acquire", probe.LeaseRequest{
			OperationID: operation,
			ResourceID:  "channel:p1",
			Generation:  1,
			ValidFrom:   from,
			ValidUntil:  until,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("acquire [%d,%d) status = %d, body = %s", from, until, rec.Code, rec.Body.String())
		}
		var body struct {
			Token domain.TokenID `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode acquire response: %v", err)
		}
		if body.Token == "" {
			t.Fatal("acquire returned an empty token")
		}
		return body.Token
	}
	wantConflict := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusConflict, rec.Body.String())
		}
		var body domain.Error
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode conflict response: %v", err)
		}
		if body.Code != domain.CodeLeaseConflict {
			t.Fatalf("error code = %s, want %s", body.Code, domain.CodeLeaseConflict)
		}
	}

	cases := []struct {
		name string
		run  func(*testing.T, http.Handler)
	}{
		{
			name: "shrinking renewal cannot expose the reserved tail",
			run: func(t *testing.T, h http.Handler) {
				token := mustAcquire(t, h, "initial", 0, 10_000)
				renew := post(t, h, "/api/v1/leases/renew", map[string]any{"token": token, "until": 5_000})
				if renew.Code != http.StatusOK && renew.Code != http.StatusConflict {
					t.Fatalf("shrinking renew status = %d, want 200 or 409; body = %s", renew.Code, renew.Body.String())
				}
				contender := post(t, h, "/api/v1/leases/acquire", probe.LeaseRequest{
					OperationID: "tail-contender", ResourceID: "channel:p1", Generation: 1,
					ValidFrom: 6_000, ValidUntil: 9_000,
				})
				wantConflict(t, contender)
			},
		},
		{
			name: "future renewal extends exclusivity",
			run: func(t *testing.T, h http.Handler) {
				token := mustAcquire(t, h, "extendable", 0, 5_000)
				renew := post(t, h, "/api/v1/leases/renew", map[string]any{"token": token, "until": 10_000})
				if renew.Code != http.StatusOK {
					t.Fatalf("extending renew status = %d, want 200; body = %s", renew.Code, renew.Body.String())
				}
				contender := post(t, h, "/api/v1/leases/acquire", probe.LeaseRequest{
					OperationID: "extended-tail-contender", ResourceID: "channel:p1", Generation: 1,
					ValidFrom: 9_000, ValidUntil: 11_000,
				})
				wantConflict(t, contender)
			},
		},
		{
			name: "future renewal still detects an existing overlap",
			run: func(t *testing.T, h http.Handler) {
				token := mustAcquire(t, h, "blocked-extension", 0, 5_000)
				mustAcquire(t, h, "blocker", 8_000, 10_000)
				renew := post(t, h, "/api/v1/leases/renew", map[string]any{"token": token, "until": 9_000})
				wantConflict(t, renew)
			},
		},
		{
			name: "release permits reacquisition",
			run: func(t *testing.T, h http.Handler) {
				token := mustAcquire(t, h, "released", 0, 10_000)
				release := post(t, h, "/api/v1/leases/release", map[string]any{"token": token})
				if release.Code != http.StatusOK {
					t.Fatalf("release status = %d, want 200; body = %s", release.Code, release.Body.String())
				}
				mustAcquire(t, h, "after-release", 0, 10_000)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			tc.run(t, httpapi.NewServer(st))
		})
	}
}
