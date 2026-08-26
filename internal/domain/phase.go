package domain

// Phase is one of the six strictly ordered sterilization-cycle stages. The
// cycle must advance through them as a prefix: preheat, replacement, heatup,
// exposure, exhaust, cooling. No stage may be skipped or overwritten.
type Phase string

const (
	PhasePreheat     Phase = "preheat"     // 预热
	PhaseReplacement Phase = "replacement" // 置换
	PhaseHeatup      Phase = "heatup"      // 升温
	PhaseExposure    Phase = "exposure"    // 暴露
	PhaseExhaust     Phase = "exhaust"     // 排汽
	PhaseCooling     Phase = "cooling"     // 冷却
)

// phaseOrder is the canonical stage prefix order.
var phaseOrder = [6]Phase{
	PhasePreheat,
	PhaseReplacement,
	PhaseHeatup,
	PhaseExposure,
	PhaseExhaust,
	PhaseCooling,
}

// PhaseRank returns the zero-based position of p in the canonical stage
// order, or -1 when p is not a valid stage.
func PhaseRank(p Phase) int {
	for i, q := range phaseOrder {
		if q == p {
			return i
		}
	}
	return -1
}

// ValidPhase reports whether p is one of the six documented stages.
func ValidPhase(p Phase) bool {
	return PhaseRank(p) >= 0
}

// PhaseAfter returns the stage that must immediately follow p, or "" when p
// is the final stage (cooling) or invalid. It enforces the strict prefix
// rule: the next submitted stage must be exactly this value.
func PhaseAfter(p Phase) Phase {
	r := PhaseRank(p)
	if r < 0 || r == len(phaseOrder)-1 {
		return ""
	}
	return phaseOrder[r+1]
}

// AllPhases returns the six stages in canonical order.
func AllPhases() []Phase {
	out := make([]Phase, len(phaseOrder))
	copy(out, phaseOrder[:])
	return out
}
