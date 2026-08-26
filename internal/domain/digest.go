package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Digest returns a deterministic SHA-256 digest over a set of ordered fields.
// Every value is formatted with an explicit type separator so that distinct
// inputs cannot collide, and the result is a stable hex string usable as an
// input summary, structure digest or idempotency request digest.
func Digest(fields ...any) string {
	h := sha256.New()
	for _, f := range fields {
		fmt.Fprintf(h, "%T:%v|", f, f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// StructureDigest summarizes the equipment structure of a plan (regions and
// positions) into a canonical string. It sorts the inputs so that the digest is
// independent of the order in which regions and positions were declared.
func StructureDigest(regions []ChamberRegion, positions []ProbePosition) string {
	regionParts := make([]string, 0, len(regions))
	for _, r := range regions {
		regionParts = append(regionParts, fmt.Sprintf("%s|%s|%s", r.ID, r.Kind, r.Name))
	}
	sort.Strings(regionParts)

	posParts := make([]string, 0, len(positions))
	for _, p := range positions {
		posParts = append(posParts, fmt.Sprintf("%s|%s|%d", p.ID, p.RegionID, p.LoadLayer))
	}
	sort.Strings(posParts)

	return Digest(regionParts, posParts)
}

// LoadDigest summarizes the load configuration (the set of occupied positions
// per load layer) into a canonical string used for stale-version detection.
func LoadDigest(positions []ProbePosition) string {
	layers := map[int][]string{}
	for _, p := range positions {
		layers[p.LoadLayer] = append(layers[p.LoadLayer], string(p.ID))
	}
	keys := make([]int, 0, len(layers))
	for k := range layers {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		ids := layers[k]
		sort.Strings(ids)
		parts = append(parts, fmt.Sprintf("%d:%v", k, ids))
	}
	return Digest(parts)
}

// Checksum computes the snapshot checksum from the projection fields so a
// restart can detect a torn or corrupted snapshot.
func Checksum(cycleID CycleID, validationID ValidationID, generation Generation, cursor Sequence, status CycleStatus) string {
	return Digest("snapshot", cycleID, validationID, generation, cursor, status)
}
