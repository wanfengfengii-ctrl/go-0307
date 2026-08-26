package store

import (
	"context"

	"lyophilizer-sterilization-validation/internal/domain"
)

// --- Resource leases ---

// InsertLease stores a mutually exclusive resource lease, failing on a
// duplicate token.
func (t *Tx) InsertLease(ctx context.Context, l domain.ResourceLease) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO leases (resource_id, operation_id, generation, token, valid_from, valid_until)
		VALUES (?, ?, ?, ?, ?, ?)`,
		l.ResourceID, l.OperationID, l.Generation, l.Token, l.ValidFrom, l.ValidUntil)
	return wrapf(err, "store: insert lease %s", l.Token)
}

// GetLease loads a lease by token, or ErrNotFound.
func (t *Tx) GetLease(ctx context.Context, token domain.TokenID) (domain.ResourceLease, error) {
	var l domain.ResourceLease
	row := t.tx.QueryRowContext(ctx, `
		SELECT resource_id, operation_id, generation, token, valid_from, valid_until
		FROM leases WHERE token = ?`, token)
	err := row.Scan(&l.ResourceID, &l.OperationID, &l.Generation, &l.Token, &l.ValidFrom, &l.ValidUntil)
	return l, mapNotFound(err)
}

// UpdateLeaseUntil extends a lease's expiry.
func (t *Tx) UpdateLeaseUntil(ctx context.Context, token domain.TokenID, until domain.LogicalTime) error {
	res, err := t.tx.ExecContext(ctx, `UPDATE leases SET valid_until = ? WHERE token = ?`, until, token)
	if err != nil {
		return wrapf(err, "store: update lease %s", token)
	}
	return requireRows(res, "lease "+string(token))
}

// DeleteLease removes a lease (release before natural expiry).
func (t *Tx) DeleteLease(ctx context.Context, token domain.TokenID) error {
	res, err := t.tx.ExecContext(ctx, `DELETE FROM leases WHERE token = ?`, token)
	if err != nil {
		return wrapf(err, "store: delete lease %s", token)
	}
	return requireRows(res, "lease "+string(token))
}

// HasOverlappingLease reports whether resource has a lease overlapping
// [from, until), excluding a token (used for renew, which overlaps itself).
func (t *Tx) HasOverlappingLease(ctx context.Context, resource domain.ResourceID, from, until domain.LogicalTime, excludeToken domain.TokenID) (bool, error) {
	return t.hasOverlap(ctx, `
		SELECT 1 FROM leases
		WHERE resource_id = ? AND token != ? AND valid_from < ? AND valid_until > ?
		LIMIT 1`, resource, excludeToken, until, from)
}

// ActiveLease returns the lease covering the given logical time for a resource
// and generation, or ErrNotFound when none is active.
func (t *Tx) ActiveLease(ctx context.Context, resource domain.ResourceID, generation domain.Generation, at domain.LogicalTime) (domain.ResourceLease, error) {
	var l domain.ResourceLease
	row := t.tx.QueryRowContext(ctx, `
		SELECT resource_id, operation_id, generation, token, valid_from, valid_until
		FROM leases WHERE resource_id = ? AND generation = ? AND valid_from <= ? AND valid_until > ?
		ORDER BY valid_from LIMIT 1`, resource, generation, at, at)
	err := row.Scan(&l.ResourceID, &l.OperationID, &l.Generation, &l.Token, &l.ValidFrom, &l.ValidUntil)
	return l, mapNotFound(err)
}

// --- Cycle events and snapshots ---

// AppendEvent appends one cycle event, enforcing the (cycle, generation, seq)
// uniqueness and returning the number of rows affected for conflict detection.
func (t *Tx) AppendEvent(ctx context.Context, ev domain.CycleEvent) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO cycle_events (cycle_id, generation, seq, phase, logical_time, operation_id, input_digest, audit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.CycleID, ev.Generation, ev.Sequence, ev.Phase, ev.LogicalTime, ev.OperationID, ev.InputDigest, boolToInt(ev.Audit))
	return wrapf(err, "store: append event %s/%d", ev.CycleID, ev.Sequence)
}

// ListEvents returns the append-only timeline for a cycle ordered by sequence.
func (t *Tx) ListEvents(ctx context.Context, cycleID domain.CycleID) ([]domain.CycleEvent, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT cycle_id, generation, seq, phase, logical_time, operation_id, input_digest, audit
		FROM cycle_events WHERE cycle_id = ? ORDER BY generation, seq`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.CycleEvent
	for rows.Next() {
		var e domain.CycleEvent
		var audit int
		if err := rows.Scan(&e.CycleID, &e.Generation, &e.Sequence, &e.Phase, &e.LogicalTime, &e.OperationID, &e.InputDigest, &audit); err != nil {
			return nil, err
		}
		e.Audit = audit != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// CurrentPhase returns the phase of the most recent event for a cycle and
// generation, or ErrNotFound if none exists.
func (t *Tx) CurrentPhase(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) (domain.Phase, error) {
	var p domain.Phase
	row := t.tx.QueryRowContext(ctx, `
		SELECT phase FROM cycle_events
		WHERE cycle_id = ? AND generation = ?
		ORDER BY seq DESC LIMIT 1`, cycleID, generation)
	err := row.Scan(&p)
	return p, mapNotFound(err)
}

// NextEventSeq returns one plus the highest event sequence for a cycle and
// generation (0 when none).
func (t *Tx) NextEventSeq(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) (domain.Sequence, error) {
	var seq domain.Sequence
	row := t.tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) FROM cycle_events
		WHERE cycle_id = ? AND generation = ?`, cycleID, generation)
	err := row.Scan(&seq)
	return seq, err
}

// LastLogicalTime returns the greatest logical time recorded for a cycle and
// generation, or 0 when none.
func (t *Tx) LastLogicalTime(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) (domain.LogicalTime, error) {
	var lt domain.LogicalTime
	row := t.tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(logical_time), 0) FROM cycle_events
		WHERE cycle_id = ? AND generation = ?`, cycleID, generation)
	err := row.Scan(&lt)
	return lt, err
}

// SaveSnapshot upserts the cycle projection at a cursor.
func (t *Tx) SaveSnapshot(ctx context.Context, s domain.CycleSnapshot) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO cycle_snapshots (cycle_id, validation_id, generation, cursor, status, checksum)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(cycle_id) DO UPDATE SET
			validation_id = excluded.validation_id,
			generation = excluded.generation,
			cursor = excluded.cursor,
			status = excluded.status,
			checksum = excluded.checksum`,
		s.CycleID, s.ValidationID, s.Generation, s.Cursor, s.Status, s.Checksum)
	return wrapf(err, "store: save snapshot %s", s.CycleID)
}

// GetSnapshot loads a cycle snapshot, or ErrNotFound.
func (t *Tx) GetSnapshot(ctx context.Context, cycleID domain.CycleID) (domain.CycleSnapshot, error) {
	var s domain.CycleSnapshot
	row := t.tx.QueryRowContext(ctx, `
		SELECT cycle_id, validation_id, generation, cursor, status, checksum
		FROM cycle_snapshots WHERE cycle_id = ?`, cycleID)
	err := row.Scan(&s.CycleID, &s.ValidationID, &s.Generation, &s.Cursor, &s.Status, &s.Checksum)
	return s, mapNotFound(err)
}

// --- Samples ---

// InsertSample stores one sample under its unique (cycle, generation, probe,
// seq) key.
func (t *Tx) InsertSample(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, s domain.Sample) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO samples (cycle_id, generation, probe_id, seq, logical_time, reading, device_receipt, valid)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cycleID, generation, s.ProbeID, s.Sequence, s.LogicalTime, s.Reading, s.DeviceReceipt, boolToInt(s.Valid))
	return wrapf(err, "store: insert sample %s/%d", s.ProbeID, s.Sequence)
}

// NextSampleSeq returns one plus the highest sequence for a probe in a cycle
// and generation (0 when none).
func (t *Tx) NextSampleSeq(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, probeID domain.ProbeID) (domain.Sequence, error) {
	var seq domain.Sequence
	row := t.tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(seq), 0) FROM samples
		WHERE cycle_id = ? AND generation = ? AND probe_id = ?`, cycleID, generation, probeID)
	err := row.Scan(&seq)
	return seq, err
}

// ListSamplesByProbe returns a probe's samples in a cycle/generation ordered by
// sequence.
func (t *Tx) ListSamplesByProbe(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, probeID domain.ProbeID) ([]domain.Sample, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT probe_id, seq, logical_time, reading, device_receipt, valid
		FROM samples WHERE cycle_id = ? AND generation = ? AND probe_id = ?
		ORDER BY seq`, cycleID, generation, probeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Sample
	for rows.Next() {
		var s domain.Sample
		var valid int
		if err := rows.Scan(&s.ProbeID, &s.Sequence, &s.LogicalTime, &s.Reading, &s.DeviceReceipt, &valid); err != nil {
			return nil, err
		}
		s.Valid = valid != 0
		out = append(out, s)
	}
	return out, rows.Err()
}

// LastSampleLogicalTime returns the greatest logical time among a probe's
// samples in a cycle/generation, or 0 when the probe has no samples.
func (t *Tx) LastSampleLogicalTime(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, probeID domain.ProbeID) (domain.LogicalTime, error) {
	var lt domain.LogicalTime
	row := t.tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(logical_time), 0) FROM samples
		WHERE cycle_id = ? AND generation = ? AND probe_id = ?`, cycleID, generation, probeID)
	err := row.Scan(&lt)
	return lt, err
}

// ListSampleSeqs returns every probe id that has samples in a cycle/generation,
// used by the calculation to know which positions produced data.
func (t *Tx) ListSampleSeqs(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) ([]domain.ProbeID, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT DISTINCT probe_id FROM samples
		WHERE cycle_id = ? AND generation = ? ORDER BY probe_id`, cycleID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ProbeID
	for rows.Next() {
		var p domain.ProbeID
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Biological indicators ---

// InsertIndicator stores a biological indicator result.
func (t *Tx) InsertIndicator(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, b domain.BiologicalIndicator) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO biological_indicators (cycle_id, generation, probe_id, position_id, result, evidence)
		VALUES (?, ?, ?, ?, ?, ?)`,
		cycleID, generation, b.ID, b.PositionID, b.Result, b.Evidence)
	return wrapf(err, "store: insert indicator %s", b.ID)
}

// GetIndicators returns biological indicators for a cycle/generation.
func (t *Tx) GetIndicators(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) ([]domain.BiologicalIndicator, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT probe_id, position_id, result, evidence
		FROM biological_indicators WHERE cycle_id = ? AND generation = ?
		ORDER BY probe_id`, cycleID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.BiologicalIndicator
	for rows.Next() {
		var b domain.BiologicalIndicator
		if err := rows.Scan(&b.ID, &b.PositionID, &b.Result, &b.Evidence); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- Device calls (retry tasks) ---

// InsertDeviceCall records a device invocation and its retry state.
func (t *Tx) InsertDeviceCall(ctx context.Context, c domain.DeviceCall) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO device_calls (operation_id, fault, retries, next_retry_at, receipt)
		VALUES (?, ?, ?, ?, ?)`,
		c.OperationID, c.Fault, c.Retries, c.NextRetryAt, c.Receipt)
	return wrapf(err, "store: insert device call %s", c.OperationID)
}

// GetDeviceCall loads a device call by operation, or ErrNotFound.
func (t *Tx) GetDeviceCall(ctx context.Context, op domain.OperationID) (domain.DeviceCall, error) {
	var c domain.DeviceCall
	row := t.tx.QueryRowContext(ctx, `
		SELECT operation_id, fault, retries, next_retry_at, receipt
		FROM device_calls WHERE operation_id = ?`, op)
	err := row.Scan(&c.OperationID, &c.Fault, &c.Retries, &c.NextRetryAt, &c.Receipt)
	return c, mapNotFound(err)
}

// UpdateDeviceCall advances a retry task's counter, next time and receipt.
func (t *Tx) UpdateDeviceCall(ctx context.Context, c domain.DeviceCall) error {
	res, err := t.tx.ExecContext(ctx, `
		UPDATE device_calls SET fault = ?, retries = ?, next_retry_at = ?, receipt = ?
		WHERE operation_id = ?`, c.Fault, c.Retries, c.NextRetryAt, c.Receipt, c.OperationID)
	if err != nil {
		return wrapf(err, "store: update device call %s", c.OperationID)
	}
	return requireRows(res, "device call "+string(c.OperationID))
}

// ListDeviceCalls returns every recorded device call ordered by operation id.
func (t *Tx) ListDeviceCalls(ctx context.Context) ([]domain.DeviceCall, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT operation_id, fault, retries, next_retry_at, receipt
		FROM device_calls ORDER BY operation_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.DeviceCall
	for rows.Next() {
		var c domain.DeviceCall
		if err := rows.Scan(&c.OperationID, &c.Fault, &c.Retries, &c.NextRetryAt, &c.Receipt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func requireRows(res interface{ RowsAffected() (int64, error) }, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
