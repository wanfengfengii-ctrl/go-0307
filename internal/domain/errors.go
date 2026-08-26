package domain

import (
	"fmt"
	"sort"
	"strings"
)

// ErrorCode is a stable, machine-readable business error code. Public tests
// assert these exact codes, so the values are part of the public contract and
// must not change.
type ErrorCode string

const (
	CodeValidationStale       ErrorCode = "VALIDATION_STALE"
	CodeIdempotencyConflict   ErrorCode = "IDEMPOTENCY_CONFLICT"
	CodeInvalidPhase          ErrorCode = "INVALID_PHASE"
	CodeCertificateExpired    ErrorCode = "CERTIFICATE_EXPIRED"
	CodeRangeInsufficient     ErrorCode = "RANGE_INSUFFICIENT"
	CodeLeaseConflict         ErrorCode = "LEASE_CONFLICT"
	CodeLeaseExpired          ErrorCode = "LEASE_EXPIRED"
	CodeGenerationMismatch    ErrorCode = "GENERATION_MISMATCH"
	CodeSequenceGap           ErrorCode = "SEQUENCE_GAP"
	CodeDuplicateKey          ErrorCode = "DUPLICATE_KEY"
	CodeStaleGeneration       ErrorCode = "STALE_GENERATION"
	CodeOverflow              ErrorCode = "OVERFLOW"
	CodeDivisionByZero        ErrorCode = "DIVISION_BY_ZERO"
	CodeNegativeInterval      ErrorCode = "NEGATIVE_INTERVAL"
	CodeMissingSample         ErrorCode = "MISSING_SAMPLE"
	CodeInsufficientLethality ErrorCode = "INSUFFICIENT_LETHALITY"
	CodeInvalidIndicator      ErrorCode = "INVALID_INDICATOR"
	CodeRetestOpen            ErrorCode = "RETEST_OPEN"
	CodeReviewerConflict      ErrorCode = "REVIEWER_CONFLICT"
	CodeUnqualifiedReviewer   ErrorCode = "UNQUALIFIED_REVIEWER"
	CodeFinalConflict         ErrorCode = "FINAL_CONFLICT"
	CodeInvalidPlan           ErrorCode = "INVALID_PLAN"
	CodeDuplicateProbe        ErrorCode = "DUPLICATE_PROBE"
	CodePositionUncovered     ErrorCode = "POSITION_UNCOVERED"
	CodeInvalidState          ErrorCode = "INVALID_STATE"
	CodeNotFound              ErrorCode = "NOT_FOUND"
)

// Error is the stable error structure returned by every JSON interface. It
// carries a machine code, the originating operation id, whether the caller may
// retry, and a deterministically sorted list of human-readable reasons.
type Error struct {
	Code        ErrorCode   `json:"code"`
	OperationID OperationID `json:"operation_id"`
	Retryable   bool        `json:"retryable"`
	Reasons     []string    `json:"reasons"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, strings.Join(e.Reasons, "; "))
}

// NewError builds an Error with its reasons deterministically sorted so that
// callers and tests can rely on a canonical, order-independent shape.
func NewError(code ErrorCode, op OperationID, retryable bool, reasons ...string) *Error {
	sorted := append([]string(nil), reasons...)
	sort.Strings(sorted)
	return &Error{
		Code:        code,
		OperationID: op,
		Retryable:   retryable,
		Reasons:     sorted,
	}
}

// WithOperation returns a copy of e whose OperationID is set to op. It is used
// to stamp a business error with the originating write's operation id.
func (e *Error) WithOperation(op OperationID) *Error {
	c := *e
	c.OperationID = op
	return &c
}
