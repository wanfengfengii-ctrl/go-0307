package tests

import (
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
)

func TestNewErrorSortsReasons(t *testing.T) {
	e := domain.NewError(domain.CodeValidationStale, "op-1", false, "zeta", "alpha", "mid")
	want := []string{"alpha", "mid", "zeta"}
	if len(e.Reasons) != len(want) {
		t.Fatalf("reasons length = %d, want %d", len(e.Reasons), len(want))
	}
	for i := range want {
		if e.Reasons[i] != want[i] {
			t.Fatalf("reasons[%d] = %s, want %s", i, e.Reasons[i], want[i])
		}
	}
}

func TestErrorShape(t *testing.T) {
	e := domain.NewError(domain.CodeLeaseConflict, "op-7", true, "interval overlap")
	if e.Code != domain.CodeLeaseConflict {
		t.Fatalf("code = %s", e.Code)
	}
	if e.OperationID != "op-7" {
		t.Fatalf("operation id = %s", e.OperationID)
	}
	if !e.Retryable {
		t.Fatalf("expected retryable")
	}
	if e.Error() == "" {
		t.Fatalf("Error() string is empty")
	}
}

func TestWithOperation(t *testing.T) {
	e := domain.NewError(domain.CodeGenerationMismatch, "op-1", false, "stale")
	e2 := e.WithOperation("op-2")
	if e.OperationID != "op-1" {
		t.Fatalf("original mutated: %s", e.OperationID)
	}
	if e2.OperationID != "op-2" {
		t.Fatalf("copy operation id = %s, want op-2", e2.OperationID)
	}
	if e2.Code != domain.CodeGenerationMismatch {
		t.Fatalf("copy code = %s", e2.Code)
	}
}
