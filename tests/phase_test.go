package tests

import (
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
)

func TestPhaseRank(t *testing.T) {
	want := map[domain.Phase]int{
		domain.PhasePreheat:     0,
		domain.PhaseReplacement: 1,
		domain.PhaseHeatup:      2,
		domain.PhaseExposure:    3,
		domain.PhaseExhaust:     4,
		domain.PhaseCooling:     5,
	}
	for p, r := range want {
		if got := domain.PhaseRank(p); got != r {
			t.Fatalf("PhaseRank(%s) = %d, want %d", p, got, r)
		}
	}
	if got := domain.PhaseRank("bogus"); got != -1 {
		t.Fatalf("PhaseRank(bogus) = %d, want -1", got)
	}
}

func TestPhaseAfterPrefix(t *testing.T) {
	steps := []struct {
		cur  domain.Phase
		next domain.Phase
	}{
		{domain.PhasePreheat, domain.PhaseReplacement},
		{domain.PhaseReplacement, domain.PhaseHeatup},
		{domain.PhaseHeatup, domain.PhaseExposure},
		{domain.PhaseExposure, domain.PhaseExhaust},
		{domain.PhaseExhaust, domain.PhaseCooling},
		{domain.PhaseCooling, ""},
	}
	for _, s := range steps {
		if got := domain.PhaseAfter(s.cur); got != s.next {
			t.Fatalf("PhaseAfter(%s) = %s, want %s", s.cur, got, s.next)
		}
	}
	if got := domain.PhaseAfter("bogus"); got != "" {
		t.Fatalf("PhaseAfter(bogus) = %s, want empty", got)
	}
}

func TestAllPhasesOrder(t *testing.T) {
	all := domain.AllPhases()
	if len(all) != 6 {
		t.Fatalf("len(AllPhases()) = %d, want 6", len(all))
	}
	for i, p := range all {
		if !domain.ValidPhase(p) {
			t.Fatalf("phase %s not valid", p)
		}
		if domain.PhaseRank(p) != i {
			t.Fatalf("AllPhases not in canonical order at index %d", i)
		}
	}
}
