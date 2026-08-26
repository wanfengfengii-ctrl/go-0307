package store

import (
	"context"

	"lyophilizer-sterilization-validation/internal/domain"
)

// --- Calculation results ---

// InsertCalculation stores one append-only calculation result for a position.
func (t *Tx) InsertCalculation(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, c domain.CalculationResult) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO calculation_results (
			cycle_id, generation, position_id, accumulated, lethality,
			min_temperature, uniformity, pressure_deviation, input_from, input_to,
			algorithm_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cycleID, generation, c.PositionID, c.Accumulated, c.Lethality,
		c.MinTemperature, c.Uniformity, c.PressureDeviation, c.InputFrom, c.InputTo,
		c.AlgorithmVersion)
	return wrapf(err, "store: insert calculation %s", c.PositionID)
}

// ListCalculations returns calculation results for a cycle/generation.
func (t *Tx) ListCalculations(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) ([]domain.CalculationResult, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT position_id, accumulated, lethality, min_temperature, uniformity,
		       pressure_deviation, input_from, input_to, algorithm_version
		FROM calculation_results WHERE cycle_id = ? AND generation = ?
		ORDER BY position_id`, cycleID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.CalculationResult
	for rows.Next() {
		var c domain.CalculationResult
		if err := rows.Scan(&c.PositionID, &c.Accumulated, &c.Lethality, &c.MinTemperature,
			&c.Uniformity, &c.PressureDeviation, &c.InputFrom, &c.InputTo, &c.AlgorithmVersion); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- Deviation cases and retest members ---

// InsertDeviation stores a deviation case, failing on a duplicate id.
func (t *Tx) InsertDeviation(ctx context.Context, d domain.DeviationCase) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO deviation_cases (id, cycle_id, generation, source, propagation, retest_generation, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.CycleID, d.Generation, d.Source, d.Propagation, d.RetestGeneration, d.Status)
	return wrapf(err, "store: insert deviation %s", d.ID)
}

// ListDeviations returns deviation cases for a cycle ordered by id.
func (t *Tx) ListDeviations(ctx context.Context, cycleID domain.CycleID) ([]domain.DeviationCase, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT id, cycle_id, generation, source, propagation, retest_generation, status
		FROM deviation_cases WHERE cycle_id = ? ORDER BY id`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.DeviationCase
	for rows.Next() {
		var d domain.DeviationCase
		if err := rows.Scan(&d.ID, &d.CycleID, &d.Generation, &d.Source, &d.Propagation, &d.RetestGeneration, &d.Status); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDeviationByPropagation loads the deviation case for a cycle whose
// propagation summary matches, or ErrNotFound.
func (t *Tx) GetDeviationByPropagation(ctx context.Context, cycleID domain.CycleID, propagation string) (domain.DeviationCase, error) {
	var d domain.DeviationCase
	row := t.tx.QueryRowContext(ctx, `
		SELECT id, cycle_id, generation, source, propagation, retest_generation, status
		FROM deviation_cases WHERE cycle_id = ? AND propagation = ?`, cycleID, propagation)
	err := row.Scan(&d.ID, &d.CycleID, &d.Generation, &d.Source, &d.Propagation, &d.RetestGeneration, &d.Status)
	return d, mapNotFound(err)
}

// InsertRetestMember stores one normalized retest member under its composite
// unique key.
func (t *Tx) InsertRetestMember(ctx context.Context, cycleID domain.CycleID, retestGeneration domain.Generation, m domain.RetestMember) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO retest_members (cycle_id, retest_generation, device, region, position, probe, generation)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cycleID, retestGeneration, m.Device, m.Region, m.Position, m.Probe, m.Generation)
	return wrapf(err, "store: insert retest member %s", m.Probe)
}

// ListRetestMembers returns the sorted, de-duplicated members of a retest set.
func (t *Tx) ListRetestMembers(ctx context.Context, cycleID domain.CycleID, retestGeneration domain.Generation) ([]domain.RetestMember, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT device, region, position, probe, generation
		FROM retest_members WHERE cycle_id = ? AND retest_generation = ?
		ORDER BY device, region, position, probe, generation`, cycleID, retestGeneration)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RetestMember
	for rows.Next() {
		var m domain.RetestMember
		if err := rows.Scan(&m.Device, &m.Region, &m.Position, &m.Probe, &m.Generation); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Reviews and final decision ---

// InsertReview stores one reviewer's conclusion, failing on a duplicate
// reviewer for the same cycle/generation.
func (t *Tx) InsertReview(ctx context.Context, cycleID domain.CycleID, generation domain.Generation, r domain.Review) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO reviews (cycle_id, generation, reviewer_id, qualified, conclusion)
		VALUES (?, ?, ?, ?, ?)`,
		cycleID, generation, r.ReviewerID, boolToInt(r.Qualified), r.Conclusion)
	return wrapf(err, "store: insert review %s", r.ReviewerID)
}

// ListReviews returns reviews for a cycle/generation ordered by reviewer id.
func (t *Tx) ListReviews(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) ([]domain.Review, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT reviewer_id, qualified, conclusion
		FROM reviews WHERE cycle_id = ? AND generation = ?
		ORDER BY reviewer_id`, cycleID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Review
	for rows.Next() {
		var r domain.Review
		var qualified int
		if err := rows.Scan(&r.ReviewerID, &qualified, &r.Conclusion); err != nil {
			return nil, err
		}
		r.Qualified = qualified != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// CommitFinal records the unique terminal decision, failing on a duplicate
// cycle id (the single-writer finality barrier).
func (t *Tx) CommitFinal(ctx context.Context, d domain.FinalDecision) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO final_decisions (cycle_id, decision, credential, operation_id)
		VALUES (?, ?, ?, ?)`,
		d.CycleID, d.Decision, d.Credential, d.OperationID)
	return wrapf(err, "store: commit final %s", d.CycleID)
}

// GetFinal loads the terminal decision for a cycle, or ErrNotFound.
func (t *Tx) GetFinal(ctx context.Context, cycleID domain.CycleID) (domain.FinalDecision, error) {
	var d domain.FinalDecision
	row := t.tx.QueryRowContext(ctx, `
		SELECT cycle_id, decision, credential, operation_id
		FROM final_decisions WHERE cycle_id = ?`, cycleID)
	err := row.Scan(&d.CycleID, &d.Decision, &d.Credential, &d.OperationID)
	return d, mapNotFound(err)
}

// --- Idempotency ---

// RecordIdempotency stores an operation's request/response digest, failing on a
// duplicate operation id.
func (t *Tx) RecordIdempotency(ctx context.Context, r domain.IdempotencyRecord) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO idempotency (operation_id, request_digest, response_digest)
		VALUES (?, ?, ?)`,
		r.OperationID, r.RequestDigest, r.ResponseDigest)
	return wrapf(err, "store: record idempotency %s", r.OperationID)
}

// GetIdempotency loads an operation's record, or ErrNotFound.
func (t *Tx) GetIdempotency(ctx context.Context, op domain.OperationID) (domain.IdempotencyRecord, error) {
	var r domain.IdempotencyRecord
	row := t.tx.QueryRowContext(ctx, `
		SELECT operation_id, request_digest, response_digest
		FROM idempotency WHERE operation_id = ?`, op)
	err := row.Scan(&r.OperationID, &r.RequestDigest, &r.ResponseDigest)
	return r, mapNotFound(err)
}
