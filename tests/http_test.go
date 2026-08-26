package tests

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lyophilizer-sterilization-validation/internal/httpapi"
)

func TestHealth(t *testing.T) {
	rec := serve(t, "GET", "/api/v1/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %q, want ok", body["status"])
	}
}

func TestVersion(t *testing.T) {
	rec := serve(t, "GET", "/api/v1/version")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["algorithm_version"] != "trapezoid-v1" {
		t.Fatalf("algorithm_version = %q", body["algorithm_version"])
	}
}

func TestIndexServed(t *testing.T) {
	rec := serve(t, "GET", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "冻干机蒸汽灭菌验证") {
		t.Fatalf("index.html body missing title")
	}
}

func TestEmbeddedAssetServed(t *testing.T) {
	rec := serve(t, "GET", "/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func serve(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := httpapi.NewHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	h.ServeHTTP(rec, req)
	return rec
}
