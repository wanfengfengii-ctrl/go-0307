package lethality

import (
	"context"
	"errors"
	"sort"
	"strings"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/store"
)

// RetestRequest opens a retest generation from a set of anomaly sources.
type RetestRequest struct {
	OperationID domain.OperationID
	CycleID     domain.CycleID
	Sources     []string
}

// positionInfo is the static description of one position used for retest
// propagation: its region and load layer, and the calibration batch of the
// probe bound to it.
type positionInfo struct {
	regionID   domain.RegionID
	regionKind domain.RegionKind
	loadLayer  int
	probeID    domain.ProbeID
	batch      string
}

// OpenRetest propagates each anomaly source along four documented relations —
// shared channel (same region), adjacent region (same region kind), shared
// calibration batch and shared load layer — into one sorted, de-duplicated
// retest set, and opens exactly one new generation per distinct summary.
//
// The snapshot read, propagation, summary, deviation/member insert and the
// generation promotion all run inside one write transaction. This is essential:
// the propagation summary depends on the bindings of the active generation, so
// the source generation, the derived members and the snapshot promotion must
// observe a single consistent state. Reading any of them outside the
// transaction would race a concurrent OpenRetest that has already promoted the
// generation, producing a divergent summary and a spurious second generation.
func (s *Service) OpenRetest(ctx context.Context, req RetestRequest) (domain.Generation, error) {
	if len(req.Sources) == 0 {
		return 0, domain.NewError(domain.CodeInvalidPlan, req.OperationID, false, "retest requires at least one source")
	}

	var retestGeneration domain.Generation
	err := s.store.InTx(ctx, func(tx *store.Tx) error {
		snap, err := tx.GetSnapshot(ctx, req.CycleID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "cycle not found")
		}
		if err != nil {
			return err
		}
		generation := snap.Generation

		plan, err := tx.GetPlan(ctx, snap.ValidationID)
		if err != nil {
			return err
		}
		bindings, err := tx.ListBindings(ctx, generation)
		if err != nil {
			return err
		}
		probes, err := tx.ListProbes(ctx)
		if err != nil {
			return err
		}

		index := buildPositionIndex(plan, bindings, probes)
		members := propagate(req.Sources, index)
		summary := retestSummary(members)

		// A distinct summary maps to at most one retest generation. In addition,
		// once the snapshot has been promoted to a retest generation, any
		// concurrent trigger of the same anomaly family must reuse that
		// generation rather than open a new one: the retest members and summary
		// are derived from the bindings of the *source* generation, which are
		// no longer the snapshot's active generation after promotion, so the
		// freshly computed summary would not match the stored one and a naive
		// check would open a spurious second generation.
		deviations, err := tx.ListDeviations(ctx, req.CycleID)
		if err != nil {
			return err
		}
		for _, d := range deviations {
			if d.Propagation == summary || d.RetestGeneration == generation {
				retestGeneration = d.RetestGeneration
				// Make sure the cycle reports the generation this retest
				// opened, even when a concurrent trigger won the race.
				return promoteRetestGeneration(ctx, tx, snap, retestGeneration)
			}
		}

		retestGeneration = generation + 1
		dev := domain.DeviationCase{
			ID:               domain.DeviationID("dev-" + summary[:16]),
			CycleID:          req.CycleID,
			Generation:       generation,
			Source:           req.Sources[0],
			Propagation:      summary,
			RetestGeneration: retestGeneration,
			Status:           domain.DeviationOpen,
		}
		if err := tx.InsertDeviation(ctx, dev); err != nil {
			if uniqueViolation(err) {
				// A concurrent trigger with the same summary lost the race.
				existing, err := tx.GetDeviationByPropagation(ctx, req.CycleID, summary)
				if err == nil {
					retestGeneration = existing.RetestGeneration
					return promoteRetestGeneration(ctx, tx, snap, retestGeneration)
				}
			}
			return err
		}
		for _, m := range members {
			if err := tx.InsertRetestMember(ctx, req.CycleID, retestGeneration, m); err != nil {
				return err
			}
		}
		// Advance the cycle projection to the retest generation so that the
		// generation reported to the operator actually takes effect: subsequent
		// stage/sample submissions with expected_generation=retestGeneration
		// are accepted rather than rejected as a generation mismatch.
		return promoteRetestGeneration(ctx, tx, snap, retestGeneration)
	})
	return retestGeneration, err
}

// promoteRetestGeneration upserts the cycle snapshot at the new retest
// generation, preserving the validation id, cursor and status and recomputing
// the checksum. It is a no-op when the snapshot already reports the target
// generation (e.g. a concurrent OpenRetest won the race).
func promoteRetestGeneration(ctx context.Context, tx *store.Tx, snap domain.CycleSnapshot, retestGeneration domain.Generation) error {
	if snap.Generation == retestGeneration {
		return nil
	}
	updated := domain.CycleSnapshot{
		CycleID:      snap.CycleID,
		ValidationID: snap.ValidationID,
		Generation:   retestGeneration,
		Cursor:       snap.Cursor,
		Status:       domain.CycleDeviated,
		Checksum:     domain.Checksum(snap.CycleID, snap.ValidationID, retestGeneration, snap.Cursor, domain.CycleDeviated),
	}
	return tx.SaveSnapshot(ctx, updated)
}

// RetestMembers returns the sorted, de-duplicated members of a retest set.
func (s *Service) RetestMembers(ctx context.Context, cycleID domain.CycleID, generation domain.Generation) ([]domain.RetestMember, error) {
	var out []domain.RetestMember
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListRetestMembers(ctx, cycleID, generation)
		return err
	})
	return out, err
}

// buildPositionIndex joins the plan geometry with bindings and probe lineage.
func buildPositionIndex(plan domain.ValidationPlan, bindings []domain.ProbeBinding, probes []domain.ProbeLineage) map[domain.PositionID]positionInfo {
	regionKind := make(map[domain.RegionID]domain.RegionKind, len(plan.Regions))
	for _, r := range plan.Regions {
		regionKind[r.ID] = r.Kind
	}
	batch := make(map[domain.ProbeID]string, len(probes))
	for _, p := range probes {
		batch[p.ID] = p.CalibrationBatch
	}
	positionProbe := make(map[domain.PositionID]domain.ProbeID, len(bindings))
	for _, b := range bindings {
		positionProbe[b.PositionID] = b.ProbeID
	}

	index := make(map[domain.PositionID]positionInfo, len(plan.Positions))
	for _, pos := range plan.Positions {
		probeID := positionProbe[pos.ID]
		index[pos.ID] = positionInfo{
			regionID:   pos.RegionID,
			regionKind: regionKind[pos.RegionID],
			loadLayer:  pos.LoadLayer,
			probeID:    probeID,
			batch:      batch[probeID],
		}
	}
	return index
}

// propagate expands each source position along the four relations.
func propagate(sources []string, index map[domain.PositionID]positionInfo) []domain.RetestMember {
	seen := make(map[domain.RetestMember]bool)
	var members []domain.RetestMember
	add := func(m domain.RetestMember) {
		if seen[m] {
			return
		}
		seen[m] = true
		members = append(members, m)
	}

	for _, src := range sources {
		srcPos := domain.PositionID(src)
		srcInfo, ok := index[srcPos]
		if !ok {
			continue
		}
		add(retestMember(srcPos, srcInfo))
		for pos, info := range index {
			if pos == srcPos {
				continue
			}
			sameChannel := info.regionID == srcInfo.regionID
			sameKind := info.regionKind == srcInfo.regionKind
			sameBatch := info.batch != "" && info.batch == srcInfo.batch
			sameLayer := info.loadLayer == srcInfo.loadLayer
			if sameChannel || sameKind || sameBatch || sameLayer {
				add(retestMember(pos, info))
			}
		}
	}

	sort.Slice(members, func(i, j int) bool {
		a, b := members[i], members[j]
		switch {
		case a.Device != b.Device:
			return a.Device < b.Device
		case a.Region != b.Region:
			return a.Region < b.Region
		case a.Position != b.Position:
			return a.Position < b.Position
		case a.Probe != b.Probe:
			return a.Probe < b.Probe
		default:
			return a.Generation < b.Generation
		}
	})
	return members
}

func retestMember(pos domain.PositionID, info positionInfo) domain.RetestMember {
	return domain.RetestMember{
		Device:     "lyo-1",
		Region:     info.regionID,
		Position:   pos,
		Probe:      info.probeID,
		Generation: domain.Generation(1),
	}
}

// retestSummary computes the canonical digest of a sorted retest set.
func retestSummary(members []domain.RetestMember) string {
	fields := make([]any, 0, len(members)*5)
	for _, m := range members {
		fields = append(fields, m.Device, m.Region, m.Position, m.Probe, m.Generation)
	}
	return domain.Digest(fields...)
}

func uniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed")
}
