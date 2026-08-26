// Package fixed implements the integer fixed-point arithmetic mandated by the
// domain rules: trapezoidal integration and half-away-from-zero rounding, with
// explicit checks for division by zero, negative intervals and 64-bit
// overflow. All values are int64 integers in the fixed-point scales defined by
// the domain package.
package fixed

import (
	"math"

	"lyophilizer-sterilization-validation/internal/domain"
)

// HalfAwayFromZero returns n divided by d, rounding half away from zero. A
// remainder whose doubled absolute value is at least |d| rounds away from
// zero; otherwise it truncates toward zero. d must not be zero.
func HalfAwayFromZero(n, d int64) (int64, error) {
	if d == 0 {
		return 0, domain.NewError(domain.CodeDivisionByZero, "", false, "divisor is zero")
	}
	q := n / d
	r := n % d
	if r == 0 {
		return q, nil
	}
	// Compare 2*|r| against |d| without overflowing int64. r has the same sign
	// as n, but the rounding direction follows the sign of the quotient, so we
	// decide "away from zero" from whether n and d share a sign.
	if abs2GE(r, d) {
		if (n > 0) == (d > 0) {
			q++
		} else {
			q--
		}
	}
	return q, nil
}

// abs2GE reports whether 2*|r| >= |d| for a non-zero remainder r of the same
// sign as n. It avoids the 2*|r| overflow by doubling carefully.
func abs2GE(r, d int64) bool {
	if r < 0 {
		r = -r
	}
	if d < 0 {
		d = -d
	}
	// 2*r >= d  <=>  r >= (d+1)/2 (integer ceiling of d/2)
	return r >= (d+1)/2
}

// Trapezoid returns the signed trapezoidal area between two samples y0 and y1
// separated by a positive interval dt: (y0+y1)*dt/2, rounded half away from
// zero. It rejects negative intervals, and checks 64-bit overflow in both the
// addition and multiplication steps.
func Trapezoid(y0, y1, dt int64) (int64, error) {
	if dt < 0 {
		return 0, domain.NewError(domain.CodeNegativeInterval, "", false, "interval must be non-negative")
	}
	sum, ok := addChecked(y0, y1)
	if !ok {
		return 0, domain.NewError(domain.CodeOverflow, "", false, "trapezoid sum overflow")
	}
	prod, ok := mulChecked(sum, dt)
	if !ok {
		return 0, domain.NewError(domain.CodeOverflow, "", false, "trapezoid product overflow")
	}
	return HalfAwayFromZero(prod, 2)
}

// addChecked returns a+b and whether it fits in int64.
func addChecked(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

// mulChecked returns a*b and whether it fits in int64.
func mulChecked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a == -1 {
		if b == math.MinInt64 {
			return 0, false
		}
		return -b, true
	}
	if b == -1 {
		if a == math.MinInt64 {
			return 0, false
		}
		return -a, true
	}
	r := a * b
	if r/b != a {
		return 0, false
	}
	return r, true
}
