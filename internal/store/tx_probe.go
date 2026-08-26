package store

import (
	"context"

	"lyophilizer-sterilization-validation/internal/domain"
)

// InsertProbe stores a probe's lineage, failing on a duplicate probe id.
func (t *Tx) InsertProbe(ctx context.Context, p domain.ProbeLineage) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO probes (
			id, type, range_min, range_max, certificate, calibration_batch,
			valid_from, valid_until, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Type, p.RangeMin, p.RangeMax, p.Certificate, p.CalibrationBatch,
		p.ValidFrom, p.ValidUntil, p.Status,
	)
	return wrapf(err, "store: insert probe %s", p.ID)
}

// GetProbe loads a probe lineage, or ErrNotFound when unknown.
func (t *Tx) GetProbe(ctx context.Context, id domain.ProbeID) (domain.ProbeLineage, error) {
	var p domain.ProbeLineage
	row := t.tx.QueryRowContext(ctx, `
		SELECT id, type, range_min, range_max, certificate, calibration_batch,
		       valid_from, valid_until, status
		FROM probes WHERE id = ?`, id)
	err := row.Scan(&p.ID, &p.Type, &p.RangeMin, &p.RangeMax, &p.Certificate,
		&p.CalibrationBatch, &p.ValidFrom, &p.ValidUntil, &p.Status)
	return p, mapNotFound(err)
}

// ListProbes returns every probe ordered by id.
func (t *Tx) ListProbes(ctx context.Context) ([]domain.ProbeLineage, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT id FROM probes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []domain.ProbeID
	for rows.Next() {
		var id domain.ProbeID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.ProbeLineage, 0, len(ids))
	for _, id := range ids {
		p, err := t.GetProbe(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// InsertBinding stores one probe-to-position binding.
func (t *Tx) InsertBinding(ctx context.Context, b domain.ProbeBinding) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO bindings (probe_id, position_id, generation, valid_from, valid_until)
		VALUES (?, ?, ?, ?, ?)`,
		b.ProbeID, b.PositionID, b.Generation, b.ValidFrom, b.ValidUntil)
	return wrapf(err, "store: insert binding %s->%s", b.ProbeID, b.PositionID)
}

// FindBinding returns the binding for a probe in a generation, or ErrNotFound.
func (t *Tx) FindBinding(ctx context.Context, probeID domain.ProbeID, generation domain.Generation) (domain.ProbeBinding, error) {
	var b domain.ProbeBinding
	row := t.tx.QueryRowContext(ctx, `
		SELECT probe_id, position_id, generation, valid_from, valid_until
		FROM bindings WHERE probe_id = ? AND generation = ?`, probeID, generation)
	err := row.Scan(&b.ProbeID, &b.PositionID, &b.Generation, &b.ValidFrom, &b.ValidUntil)
	return b, mapNotFound(err)
}

// ListBindings returns every binding for a generation, ordered by probe id.
func (t *Tx) ListBindings(ctx context.Context, generation domain.Generation) ([]domain.ProbeBinding, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT probe_id, position_id, generation, valid_from, valid_until
		FROM bindings WHERE generation = ? ORDER BY probe_id`, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ProbeBinding
	for rows.Next() {
		var b domain.ProbeBinding
		if err := rows.Scan(&b.ProbeID, &b.PositionID, &b.Generation, &b.ValidFrom, &b.ValidUntil); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// HasOverlappingBindingPosition reports whether positionID already has a binding
// whose interval overlaps [from, until), which forbids two probes on one
// position at the same logical time.
func (t *Tx) HasOverlappingBindingPosition(ctx context.Context, positionID domain.PositionID, from, until domain.LogicalTime) (bool, error) {
	return t.hasOverlap(ctx, `SELECT 1 FROM bindings WHERE position_id = ? AND valid_from < ? AND valid_until > ? LIMIT 1`,
		positionID, until, from)
}

// HasOverlappingBindingProbe reports whether probeID is bound to any other
// position whose interval overlaps [from, until).
func (t *Tx) HasOverlappingBindingProbe(ctx context.Context, probeID domain.ProbeID, from, until domain.LogicalTime, excludePosition domain.PositionID) (bool, error) {
	return t.hasOverlap(ctx, `SELECT 1 FROM bindings WHERE probe_id = ? AND position_id != ? AND valid_from < ? AND valid_until > ? LIMIT 1`,
		probeID, excludePosition, until, from)
}

func (t *Tx) hasOverlap(ctx context.Context, query string, args ...any) (bool, error) {
	var one int
	err := t.tx.QueryRowContext(ctx, query, args...).Scan(&one)
	if err != nil {
		if mapNotFound(err) == ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
