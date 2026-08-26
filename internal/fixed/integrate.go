package fixed

import "lyophilizer-sterilization-validation/internal/domain"

// Point is one (value, time) reading in a deterministic series ordered by
// logical time. Value is in the natural fixed-point scale of the series
// (temperature in milli-celsius, pressure in pascals).
type Point struct {
	Value int64
	Time  int64
}

// Integrate returns the trapezoidal integral of (value - baseline) over the
// series. Each segment uses the half-away-from-zero trapezoid rule and every
// intermediate sum is checked for 64-bit overflow. Out-of-order times (a
// negative segment interval) are rejected with NEGATIVE_INTERVAL.
func Integrate(points []Point, baseline int64) (int64, error) {
	if len(points) < 2 {
		return 0, domain.NewError(domain.CodeMissingSample, "", false, "need at least two points to integrate")
	}
	var total int64
	for i := 1; i < len(points); i++ {
		dt := points[i].Time - points[i-1].Time
		if dt < 0 {
			return 0, domain.NewError(domain.CodeNegativeInterval, "", false, "points out of time order")
		}
		y0 := points[i-1].Value - baseline
		y1 := points[i].Value - baseline
		seg, err := Trapezoid(y0, y1, dt)
		if err != nil {
			return 0, err
		}
		var ok bool
		total, ok = addChecked(total, seg)
		if !ok {
			return 0, domain.NewError(domain.CodeOverflow, "", false, "integral accumulation overflow")
		}
	}
	return total, nil
}

// Uniformity returns max-min over values, or an error for an empty input.
func Uniformity(values []int64) (int64, error) {
	if len(values) == 0 {
		return 0, domain.NewError(domain.CodeMissingSample, "", false, "no values for uniformity")
	}
	mn, mx := values[0], values[0]
	for _, v := range values[1:] {
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	return mx - mn, nil
}

// MinValue returns the minimum of values, or an error for an empty input.
func MinValue(values []int64) (int64, error) {
	if len(values) == 0 {
		return 0, domain.NewError(domain.CodeMissingSample, "", false, "no values")
	}
	mn := values[0]
	for _, v := range values[1:] {
		if v < mn {
			mn = v
		}
	}
	return mn, nil
}

// BaseEquivalent converts an accumulated thermal dose (excess milli-celsius ×
// milliseconds) into base-temperature-equivalent lethality in the supplied
// scale (millionths of a minute), rounding half away from zero. It rejects a
// non-positive reference dose and 64-bit overflow in the scaling step.
func BaseEquivalent(accumulated, referencePerMinute, scale int64) (int64, error) {
	if referencePerMinute <= 0 {
		return 0, domain.NewError(domain.CodeDivisionByZero, "", false, "reference dose must be positive")
	}
	num, ok := mulChecked(accumulated, scale)
	if !ok {
		return 0, domain.NewError(domain.CodeOverflow, "", false, "base-equivalent multiplication overflow")
	}
	return HalfAwayFromZero(num, referencePerMinute)
}
