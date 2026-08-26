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

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/httpapi"
)

func TestModel_RetestGenerationIsolation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	setupRetest(t, ctx, st)
	handler := httpapi.NewServer(st)

	request := func(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var encoded bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&encoded).Encode(body); err != nil {
				t.Fatalf("encode request: %v", err)
			}
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, &encoded)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want %d; body = %s", method, target, rec.Code, http.StatusOK, rec.Body.String())
		}
		return rec
	}

	openCases := []struct {
		name       string
		operation  string
		source     string
		generation domain.Generation
	}{
		{name: "first propagation", operation: "op-model-p1", source: "p1", generation: 2},
		{name: "same propagation is idempotent", operation: "op-model-p1-again", source: "p1", generation: 2},
		{name: "different propagation increments", operation: "op-model-p3", source: "p3", generation: 3},
	}
	for _, tc := range openCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := request(t, http.MethodPost, "/api/v1/cycles/c1/deviations", map[string]any{
				"OperationID": tc.operation,
				"Sources":     []string{tc.source},
			})
			var response struct {
				Generation domain.Generation `json:"retest_generation"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Generation != tc.generation {
				t.Fatalf("retest_generation = %d, want %d", response.Generation, tc.generation)
			}
		})
	}

	memberCases := []struct {
		name       string
		generation domain.Generation
		positions  []domain.PositionID
	}{
		{name: "first generation contains only first propagation", generation: 2, positions: []domain.PositionID{"p1", "p2"}},
		{name: "second generation contains only second propagation", generation: 3, positions: []domain.PositionID{"p3", "p4"}},
	}
	for _, tc := range memberCases {
		t.Run(tc.name, func(t *testing.T) {
			target := fmt.Sprintf("/api/v1/cycles/c1/retests?generation=%d", tc.generation)
			rec := request(t, http.MethodGet, target, nil)
			var members []domain.RetestMember
			if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
				t.Fatalf("decode members: %v", err)
			}
			positions := make([]domain.PositionID, len(members))
			for i, member := range members {
				positions[i] = member.Position
			}
			if !reflect.DeepEqual(positions, tc.positions) {
				t.Fatalf("positions = %v, want sorted isolated set %v", positions, tc.positions)
			}
		})
	}
}
