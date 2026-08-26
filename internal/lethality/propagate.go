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
func (s *Service) OpenRetest(ctx context.Context, req RetestRequest) (domain.Generation, error) {
	if len(req.Sources) == 0 {
		return 0, domain.NewError(domain.CodeInvalidPlan, req.OperationID, false, "retest requires at least one source")
	}

	var (
		generation   domain.Generation
		validationID domain.ValidationID
		plan         domain.ValidationPlan
		bindings     []domain.ProbeBinding
		probes       []domain.ProbeLineage
	)
	err := s.store.Read(ctx, func(tx *store.Tx) error {
		snap, err := tx.GetSnapshot(ctx, req.CycleID)
		if errors.Is(err, store.ErrNotFound) {
			return domain.NewError(domain.CodeNotFound, "", false, "cycle not found")
		}
		if err != nil {
			return err
		}
		generation = snap.Generation
		validationID = snap.ValidationID

		plan, err = tx.GetPlan(ctx, validationID)
		if err != nil {
			return err
		}
		bindings, err = tx.ListBindings(ctx, generation)
		if err != nil {
			return err
		}
		probes, err = tx.ListProbes(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}

	index := buildPositionIndex(plan, bindings, probes)
	members := propagate(req.Sources, index)
	summary := retestSummary(members)

	var retestGeneration domain.Generation
	err = s.store.InTx(ctx, func(tx *store.Tx) error {
		// A distinct summary maps to at most one retest generation.
		deviations, err := tx.ListDeviations(ctx, req.CycleID)
		if err != nil {
			return err
		}
		for _, d := range deviations {
			if d.Propagation == summary {
				retestGeneration = d.RetestGeneration
				return nil
			}
		}
		// Each distinct propagation summary gets its own strictly increasing
		// retest generation, independent of the cycle's current generation.
		// Basing the next generation only on the snapshot generation would hand
		// every retest the same value (generation+1), so two retests opened
		// from different sources would share a generation and their members
		// would be indistinguishable. Floor at the cycle generation so the first
		// retest stays generation+1, then advance past any existing retest
		// generation already assigned to this cycle.
		maxRetest := generation
		for _, d := range deviations {
			if d.RetestGeneration > maxRetest {
				maxRetest = d.RetestGeneration
			}
		}
		retestGeneration = maxRetest + 1
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
					return nil
				}
			}
			return err
		}
		for _, m := range members {
			if err := tx.InsertRetestMember(ctx, req.CycleID, retestGeneration, m); err != nil {
				return err
			}
		}
		return nil
	})
	return retestGeneration, err
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
