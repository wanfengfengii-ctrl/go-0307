package tests

import (
	"math"
	"testing"

	"lyophilizer-sterilization-validation/internal/domain"
	"lyophilizer-sterilization-validation/internal/fixed"
)

func TestHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		name string
		n, d int64
		want int64
	}{
		{"exact", 4, 2, 2},
		{"round up", 5, 2, 3},
		{"round half up", 3, 2, 2},
		{"round half negative", -3, 2, -2},
		{"negative away", -5, 2, -3},
		{"single half", 1, 2, 1},
		{"negative single half", -1, 2, -1},
		{"negative divisor", 5, -2, -3},
		{"zero numerator", 0, 7, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fixed.HalfAwayFromZero(c.n, c.d)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("HalfAwayFromZero(%d,%d) = %d, want %d", c.n, c.d, got, c.want)
			}
		})
	}
}

func TestHalfAwayFromZeroDivisionByZero(t *testing.T) {
	_, err := fixed.HalfAwayFromZero(1, 0)
	assertCode(t, err, domain.CodeDivisionByZero)
}

func TestTrapezoid(t *testing.T) {
	cases := []struct {
		name       string
		y0, y1, dt int64
		want       int64
	}{
		{"zero base", 0, 2, 1, 1},
		{"square", 1, 3, 2, 4},
		{"round half away", 1, 2, 1, 2},
		{"zero interval", 5, 9, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := fixed.Trapezoid(c.y0, c.y1, c.dt)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("Trapezoid(%d,%d,%d) = %d, want %d", c.y0, c.y1, c.dt, got, c.want)
			}
		})
	}
}

func TestTrapezoidNegativeInterval(t *testing.T) {
	_, err := fixed.Trapezoid(1, 2, -1)
	assertCode(t, err, domain.CodeNegativeInterval)
}

func TestTrapezoidSumOverflow(t *testing.T) {
	_, err := fixed.Trapezoid(math.MaxInt64, 1, 1)
	assertCode(t, err, domain.CodeOverflow)
}

func TestTrapezoidProductOverflow(t *testing.T) {
	_, err := fixed.Trapezoid(math.MaxInt64/2, math.MaxInt64/2, 3)
	assertCode(t, err, domain.CodeOverflow)
}

func assertCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	de, ok := err.(*domain.Error)
	if !ok {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if de.Code != want {
		t.Fatalf("got code %s, want %s", de.Code, want)
	}
}
