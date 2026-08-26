package store

import (
	"context"

	"lyophilizer-sterilization-validation/internal/domain"
)

// GetPlan loads a plan together with its regions and positions, or returns
// ErrNotFound when the validation id is unknown.
func (t *Tx) GetPlan(ctx context.Context, id domain.ValidationID) (domain.ValidationPlan, error) {
	var p domain.ValidationPlan
	row := t.tx.QueryRowContext(ctx, `
		SELECT id, generation, structure_digest, load_digest,
		       exposure_min_temp, exposure_min_pressure, exposure_max_pressure,
		       exposure_min_duration, sampling_interval, lethality_threshold,
		       locked_at, status
		FROM plans WHERE id = ?`, id)
	if err := row.Scan(
		&p.ID, &p.Generation, &p.StructureDigest, &p.LoadDigest,
		&p.Exposure.MinTemperature, &p.Exposure.MinPressure, &p.Exposure.MaxPressure,
		&p.Exposure.MinDuration, &p.SamplingInterval, &p.LethalityThreshold,
		&p.LockedAt, &p.Status,
	); err != nil {
		return p, mapNotFound(err)
	}

	regions, err := t.listRegions(ctx, id)
	if err != nil {
		return p, err
	}
	p.Regions = regions

	positions, err := t.listPositions(ctx, id)
	if err != nil {
		return p, err
	}
	p.Positions = positions

	summaries, err := t.listProbeSummaries(ctx, id)
	if err != nil {
		return p, err
	}
	p.ProbeSummaries = summaries
	return p, nil
}

// ListPlans returns every locked plan ordered by id for stable listing.
func (t *Tx) ListPlans(ctx context.Context) ([]domain.ValidationPlan, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT id FROM plans ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []domain.ValidationID
	for rows.Next() {
		var id domain.ValidationID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]domain.ValidationPlan, 0, len(ids))
	for _, id := range ids {
		p, err := t.GetPlan(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// LockPlan stores the latest generation of a validation plan and its regions,
// positions and probe summaries in one unit of work. Re-locking a changed plan
// upserts the latest generation and replaces the associated structure, all
// atomically: any failure rolls back every write.
func (t *Tx) LockPlan(ctx context.Context, p domain.ValidationPlan) error {
	if _, err := t.tx.ExecContext(ctx, `
		INSERT INTO plans (
			id, generation, structure_digest, load_digest,
			exposure_min_temp, exposure_min_pressure, exposure_max_pressure,
			exposure_min_duration, sampling_interval, lethality_threshold,
			locked_at, status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			generation = excluded.generation,
			structure_digest = excluded.structure_digest,
			load_digest = excluded.load_digest,
			exposure_min_temp = excluded.exposure_min_temp,
			exposure_min_pressure = excluded.exposure_min_pressure,
			exposure_max_pressure = excluded.exposure_max_pressure,
			exposure_min_duration = excluded.exposure_min_duration,
			sampling_interval = excluded.sampling_interval,
			lethality_threshold = excluded.lethality_threshold,
			locked_at = excluded.locked_at,
			status = excluded.status`,
		p.ID, p.Generation, p.StructureDigest, p.LoadDigest,
		p.Exposure.MinTemperature, p.Exposure.MinPressure, p.Exposure.MaxPressure,
		p.Exposure.MinDuration, p.SamplingInterval, p.LethalityThreshold,
		p.LockedAt, p.Status,
	); err != nil {
		return wrapf(err, "store: insert plan %s", p.ID)
	}
	// Replace the associated structure so a new generation does not retain stale
	// regions, positions or probe summaries.
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM plan_regions WHERE plan_id = ?`, p.ID); err != nil {
		return wrapf(err, "store: clear regions for %s", p.ID)
	}
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM plan_positions WHERE plan_id = ?`, p.ID); err != nil {
		return wrapf(err, "store: clear positions for %s", p.ID)
	}
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM plan_probe_summaries WHERE plan_id = ?`, p.ID); err != nil {
		return wrapf(err, "store: clear probe summaries for %s", p.ID)
	}
	for _, r := range p.Regions {
		if err := t.insertRegion(ctx, p.ID, r); err != nil {
			return err
		}
	}
	for _, pos := range p.Positions {
		if err := t.insertPosition(ctx, p.ID, pos); err != nil {
			return err
		}
	}
	for _, ps := range p.ProbeSummaries {
		if err := t.insertProbeSummary(ctx, p.ID, ps); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tx) insertRegion(ctx context.Context, planID domain.ValidationID, r domain.ChamberRegion) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO plan_regions (plan_id, region_id, name, kind)
		VALUES (?, ?, ?, ?)`, planID, r.ID, r.Name, r.Kind)
	return wrapf(err, "store: insert region %s", r.ID)
}

func (t *Tx) insertPosition(ctx context.Context, planID domain.ValidationID, p domain.ProbePosition) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO plan_positions (plan_id, position_id, region_id, load_layer)
		VALUES (?, ?, ?, ?)`, planID, p.ID, p.RegionID, p.LoadLayer)
	return wrapf(err, "store: insert position %s", p.ID)
}

func (t *Tx) insertProbeSummary(ctx context.Context, planID domain.ValidationID, ps domain.ProbeSummary) error {
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO plan_probe_summaries (plan_id, probe_id, position_id, certificate)
		VALUES (?, ?, ?, ?)`, planID, ps.ProbeID, ps.PositionID, ps.Certificate)
	return wrapf(err, "store: insert probe summary %s", ps.ProbeID)
}

func (t *Tx) listRegions(ctx context.Context, planID domain.ValidationID) ([]domain.ChamberRegion, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT region_id, name, kind FROM plan_regions
		WHERE plan_id = ? ORDER BY region_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ChamberRegion
	for rows.Next() {
		var r domain.ChamberRegion
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *Tx) listPositions(ctx context.Context, planID domain.ValidationID) ([]domain.ProbePosition, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT position_id, region_id, load_layer FROM plan_positions
		WHERE plan_id = ? ORDER BY position_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ProbePosition
	for rows.Next() {
		var p domain.ProbePosition
		if err := rows.Scan(&p.ID, &p.RegionID, &p.LoadLayer); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (t *Tx) listProbeSummaries(ctx context.Context, planID domain.ValidationID) ([]domain.ProbeSummary, error) {
	rows, err := t.tx.QueryContext(ctx, `
		SELECT probe_id, position_id, certificate FROM plan_probe_summaries
		WHERE plan_id = ? ORDER BY probe_id`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.ProbeSummary
	for rows.Next() {
		var ps domain.ProbeSummary
		if err := rows.Scan(&ps.ProbeID, &ps.PositionID, &ps.Certificate); err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}
