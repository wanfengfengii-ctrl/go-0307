// Package domain holds the stable, cross-cutting domain types shared by every
// business component described in the project document. These types are the
// immutable vocabulary of the freeze-dryer steam sterilization workflow:
// identifiers, fixed-point scales, phases, and the persisted data records.
package domain

// Identifier types provide compile-time distinction between the many
// string-keyed entities in the workflow. Each corresponds to a documented
// data-model key (cycle, generation, probe, position, region, resource,
// operation, token).
type (
	ValidationID string
	CycleID      string
	ProbeID      string
	PositionID   string
	RegionID     string
	ResourceID   string
	OperationID  string
	TokenID      string
	DeviationID  string
)

// Generation identifies one immutable validation iteration. Locking a plan,
// changing structure/load/probes/thresholds, or opening a retest always
// produces a new, strictly increasing generation.
type Generation int64

// LogicalTime is the explicit logical clock value, expressed in milliseconds.
// The domain never depends on wall time for correctness; all ordering uses
// this monotonically advancing value.
type LogicalTime int64

// Sequence is the per-probe, per-cycle sample sequence number. Sample keys
// (cycle, generation, probe, sequence) must be unique and each probe's
// sequence must strictly increase.
type Sequence int64

// Fixed-point scales mandated by the domain rules. Temperature uses
// thousandths of a degree Celsius, pressure uses pascals, time uses
// milliseconds, and lethality uses millionths of a minute.
const (
	TemperatureScale = 1000      // °C, in milli-celsius
	PressureScale    = 1         // Pa, native
	TimeScale        = 1         // ms, native
	LethalityScale   = 1_000_000 // minute, in millionths
)
