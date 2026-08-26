// Package probe is the "probe lineage and lease" component. It maintains probe,
// certificate, biological-indicator and position relationships, and grants
// mutually exclusive logical-time leases for calibration slots and acquisition
// channels, using unique constraints and conditional updates for arbitration.
package probe

import (
	"context"
	"errors"
	"strings"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// Service is the concrete probe-lineage and lease boundary.
type Service struct {
	store *store.Store
}

// NewService constructs a probe service over s.
func NewService(s *store.Store) *Service { return &Service{store: s} }

// Register establishes a probe's per-piece identity (type, range, certificate
// and calibration batch). Duplicate probe ids are rejected.
func (s *Service) Register(ctx context.Context, p domain.ProbeLineage) error {
	if p.ID == "" {
		return domain.NewError(domain.CodeInvalidPlan, "", false, "probe id is empty")
	}
	if p.RangeMin >= p.RangeMax {
		return domain.NewError(domain.CodeRangeInsufficient, "", false, "probe range is empty or inverted")
	}
	if p.ValidFrom >= p.ValidUntil {
		return domain.NewError(domain.CodeCertificateExpired, "", false, "probe validity interval is empty")
	}
	p.Status = domain.ProbeActive
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertProbe(ctx, p); err != nil {
			if uniqueViolation(err) {
				return domain.NewError(domain.CodeDuplicateKey, "", false, "probe "+string(p.ID)+" already registered")
			}
			return err
		}
		return nil
	})
}

// Get returns a probe's lineage, or a NOT_FOUND error.
func (s *Service) Get(ctx context.Context, id domain.ProbeID) (domain.ProbeLineage, error) {
	var p domain.ProbeLineage
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		p, err = tx.GetProbe(ctx, id)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return p, domain.NewError(domain.CodeNotFound, "", false, "probe not found")
	}
	return p, err
}

// List returns every registered probe ordered by id.
func (s *Service) List(ctx context.Context) ([]domain.ProbeLineage, error) {
	var out []domain.ProbeLineage
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListProbes(ctx)
		return err
	})
	return out, err
}

// BindRequest atomically binds a probe to a position for a generation.
type BindRequest struct {
	OperationID domain.OperationID
	ProbeID     domain.ProbeID
	PositionID  domain.PositionID
	Generation  domain.Generation
	ValidFrom   domain.LogicalTime
	ValidUntil  domain.LogicalTime
	RangeMin    int64
	RangeMax    int64
}

// Bind validates calibration validity and range coverage, then atomically
// binds the probe and records the event, rolling back on any conflict.
func (s *Service) Bind(ctx context.Context, req BindRequest) error {
	if req.ValidFrom >= req.ValidUntil {
		return domain.NewError(domain.CodeNegativeInterval, req.OperationID, false, "binding interval must be non-empty")
	}
	requestDigest := domain.Digest("bind", req.ProbeID, req.PositionID, req.Generation, req.ValidFrom, req.ValidUntil, req.RangeMin, req.RangeMax)

	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if rec, err := tx.GetIdempotency(ctx, req.OperationID); err == nil {
			if rec.RequestDigest != requestDigest {
				return domain.NewError(domain.CodeIdempotencyConflict, req.OperationID, false, "operation reused with different content")
			}
			return nil // idempotent replay
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		p, err := tx.GetProbe(ctx, req.ProbeID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, req.OperationID, false, "probe not found")
		}
		if err != nil {
			return err
		}
		// Certificate must cover the entire binding interval.
		if p.ValidFrom > req.ValidFrom || p.ValidUntil < req.ValidUntil {
			return domain.NewError(domain.CodeCertificateExpired, req.OperationID, false, "probe certificate does not cover binding interval")
		}
		// Range must cover the required measurement range.
		if p.RangeMin > req.RangeMin || p.RangeMax < req.RangeMax {
			return domain.NewError(domain.CodeRangeInsufficient, req.OperationID, false, "probe range does not cover required range")
		}

		overlapPos, err := tx.HasOverlappingBindingPosition(ctx, req.PositionID, req.ValidFrom, req.ValidUntil)
		if err != nil {
			return err
		}
		if overlapPos {
			return domain.NewError(domain.CodeDuplicateKey, req.OperationID, false, "position already bound in interval")
		}
		overlapProbe, err := tx.HasOverlappingBindingProbe(ctx, req.ProbeID, req.ValidFrom, req.ValidUntil, req.PositionID)
		if err != nil {
			return err
		}
		if overlapProbe {
			return domain.NewError(domain.CodeDuplicateKey, req.OperationID, false, "probe already bound to another position")
		}

		if err := tx.InsertBinding(ctx, domain.ProbeBinding{
			ProbeID:    req.ProbeID,
			PositionID: req.PositionID,
			Generation: req.Generation,
			ValidFrom:  req.ValidFrom,
			ValidUntil: req.ValidUntil,
		}); err != nil {
			return err
		}
		return tx.RecordIdempotency(ctx, domain.IdempotencyRecord{
			OperationID:    req.OperationID,
			RequestDigest:  requestDigest,
			ResponseDigest: "bound",
		})
	})
}

// LeaseRequest acquires a resource lease.
type LeaseRequest struct {
	OperationID domain.OperationID
	ResourceID  domain.ResourceID
	Generation  domain.Generation
	ValidFrom   domain.LogicalTime
	ValidUntil  domain.LogicalTime
}

// Acquire grants a mutually exclusive lease, failing on interval overlap. The
// token is deterministic so retries and tests are reproducible.
func (s *Service) Acquire(ctx context.Context, req LeaseRequest) (domain.TokenID, error) {
	if req.ValidFrom >= req.ValidUntil {
		return "", domain.NewError(domain.CodeNegativeInterval, req.OperationID, false, "lease interval must be non-empty")
	}
	token := domain.TokenID(domain.Digest("lease", req.ResourceID, req.OperationID, req.Generation, req.ValidFrom, req.ValidUntil)[:24])

	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		overlap, err := tx.HasOverlappingLease(ctx, req.ResourceID, req.ValidFrom, req.ValidUntil, "")
		if err != nil {
			return err
		}
		if overlap {
			return domain.NewError(domain.CodeLeaseConflict, req.OperationID, false, "resource lease interval overlaps an existing lease")
		}
		return tx.InsertLease(ctx, domain.ResourceLease{
			ResourceID:  req.ResourceID,
			OperationID: req.OperationID,
			Generation:  req.Generation,
			Token:       token,
			ValidFrom:   req.ValidFrom,
			ValidUntil:  req.ValidUntil,
		})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Renew extends a lease held by the same token.
func (s *Service) Renew(ctx context.Context, token domain.TokenID, until domain.LogicalTime) error {
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		l, err := tx.GetLease(ctx, token)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "lease not found")
		}
		if err != nil {
			return err
		}
		// Renewal may only extend or keep the granted interval; a smaller
		// endpoint would silently release the reserved trailing segment.
		if until < l.ValidUntil {
			return domain.NewError(domain.CodeNegativeInterval, "", false, "renewal endpoint shrinks granted lease interval")
		}
		overlap, err := tx.HasOverlappingLease(ctx, l.ResourceID, l.ValidUntil, until, token)
		if err != nil {
			return err
		}
		if overlap {
			return domain.NewError(domain.CodeLeaseConflict, "", false, "renewal overlaps another lease")
		}
		return tx.UpdateLeaseUntil(ctx, token, until)
	})
}

// Release ends a lease before its natural expiry.
func (s *Service) Release(ctx context.Context, token domain.TokenID) error {
	return s.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.DeleteLease(ctx, token); errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "lease not found")
		} else if err != nil {
			return err
		}
		return nil
	})
}

func uniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed")
}
