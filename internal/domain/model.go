package domain

// The types in this file are the persisted data records from the project's
// data model. They are append-only facts: once a plan is locked or an event is
// recorded, the record is never mutated in place. JSON tags expose them through
// the public HTTP interface with a stable snake_case contract.

// RegionKind classifies a chamber region of the freeze-dryer.
type RegionKind string

const (
	RegionChamber   RegionKind = "chamber"
	RegionShelf     RegionKind = "shelf"
	RegionCondenser RegionKind = "condenser"
	RegionDrain     RegionKind = "drain"
)

// ChamberRegion describes one structural region of the equipment.
type ChamberRegion struct {
	ID   RegionID   `json:"id"`
	Name string     `json:"name"`
	Kind RegionKind `json:"kind"`
}

// ProbePosition maps a probe location to a chamber region and a load layer.
type ProbePosition struct {
	ID        PositionID `json:"id"`
	RegionID  RegionID   `json:"region_id"`
	LoadLayer int        `json:"load_layer"`
}

// ProbeSummary is the plan-level declaration that a probe is assigned to a
// position for a validation, carrying the calibration certificate summary. It
// is frozen with the plan and revalidated for uniqueness and coverage.
type ProbeSummary struct {
	ProbeID     ProbeID    `json:"probe_id"`
	PositionID  PositionID `json:"position_id"`
	Certificate string     `json:"certificate"`
}

// ExposureRule holds the temperature, pressure and duration boundaries that
// the exposure stage must satisfy.
type ExposureRule struct {
	MinTemperature int64       `json:"min_temperature"`
	MinPressure    int64       `json:"min_pressure"`
	MaxPressure    int64       `json:"max_pressure"`
	MinDuration    LogicalTime `json:"min_duration"`
}

// PlanStatus is the lifecycle state of a locked validation plan.
type PlanStatus string

const (
	PlanLocked PlanStatus = "locked"
	PlanStale  PlanStatus = "stale"
)

// ValidationPlan is the immutable, locked definition of one validation
// iteration: equipment structure, load layout, phase parameters, sampling
// frequency and lethality threshold.
type ValidationPlan struct {
	ID                 ValidationID    `json:"id"`
	Generation         Generation      `json:"generation"`
	StructureDigest    string          `json:"structure_digest"`
	LoadDigest         string          `json:"load_digest"`
	Regions            []ChamberRegion `json:"regions"`
	Positions          []ProbePosition `json:"positions"`
	ProbeSummaries     []ProbeSummary  `json:"probe_summaries"`
	Exposure           ExposureRule    `json:"exposure"`
	SamplingInterval   LogicalTime     `json:"sampling_interval"`
	LethalityThreshold int64           `json:"lethality_threshold"`
	LockedAt           LogicalTime     `json:"locked_at"`
	Status             PlanStatus      `json:"status"`
}

// ProbeType distinguishes temperature, pressure and biological-indicator
// probes, which have different ranges and calibration semantics.
type ProbeType string

const (
	ProbeTemperature ProbeType = "temperature"
	ProbePressure    ProbeType = "pressure"
	ProbeBiological  ProbeType = "biological_indicator"
)

// ProbeStatus is the lifecycle state of a probe's lineage.
type ProbeStatus string

const (
	ProbeActive  ProbeStatus = "active"
	ProbeExpired ProbeStatus = "expired"
	ProbeRetired ProbeStatus = "retired"
)

// ProbeLineage records a probe's identity, type, measurement range and
// calibration certificate summary.
type ProbeLineage struct {
	ID               ProbeID     `json:"id"`
	Type             ProbeType   `json:"type"`
	RangeMin         int64       `json:"range_min"`
	RangeMax         int64       `json:"range_max"`
	Certificate      string      `json:"certificate"`
	CalibrationBatch string      `json:"calibration_batch"`
	ValidFrom        LogicalTime `json:"valid_from"`
	ValidUntil       LogicalTime `json:"valid_until"`
	Status           ProbeStatus `json:"status"`
}

// ProbeBinding records the atomic binding of a probe to a position for a
// generation within a logical time interval.
type ProbeBinding struct {
	ProbeID    ProbeID     `json:"probe_id"`
	PositionID PositionID  `json:"position_id"`
	Generation Generation  `json:"generation"`
	ValidFrom  LogicalTime `json:"valid_from"`
	ValidUntil LogicalTime `json:"valid_until"`
}

// ResourceLease grants a calibration slot or acquisition channel to one
// operation for a mutually exclusive logical time interval.
type ResourceLease struct {
	ResourceID  ResourceID  `json:"resource_id"`
	OperationID OperationID `json:"operation_id"`
	Generation  Generation  `json:"generation"`
	Token       TokenID     `json:"token"`
	ValidFrom   LogicalTime `json:"valid_from"`
	ValidUntil  LogicalTime `json:"valid_until"`
}

// CycleEvent is one append-only fact in the cycle timeline. Audit events (such
// as a stale-generation sample that must not change the current result) are
// marked so they remain in the timeline without advancing the projection.
type CycleEvent struct {
	CycleID     CycleID     `json:"cycle_id"`
	Generation  Generation  `json:"generation"`
	Sequence    Sequence    `json:"sequence"`
	Phase       Phase       `json:"phase"`
	LogicalTime LogicalTime `json:"logical_time"`
	OperationID OperationID `json:"operation_id"`
	InputDigest string      `json:"input_digest"`
	Audit       bool        `json:"audit"`
}

// CycleStatus is the lifecycle state of a cycle aggregate.
type CycleStatus string

const (
	CycleOpen     CycleStatus = "open"
	CycleComplete CycleStatus = "complete"
	CycleDeviated CycleStatus = "deviated"
)

// CycleSnapshot is the recoverable projection of a cycle at a specific event
// cursor, protected by a checksum.
type CycleSnapshot struct {
	CycleID      CycleID      `json:"cycle_id"`
	ValidationID ValidationID `json:"validation_id"`
	Generation   Generation   `json:"generation"`
	Cursor       Sequence     `json:"cursor"`
	Status       CycleStatus  `json:"status"`
	Checksum     string       `json:"checksum"`
}

// Sample is one integer reading from a probe at a logical time.
type Sample struct {
	ProbeID       ProbeID     `json:"probe_id"`
	Sequence      Sequence    `json:"sequence"`
	LogicalTime   LogicalTime `json:"logical_time"`
	Reading       int64       `json:"reading"`
	DeviceReceipt string      `json:"device_receipt"`
	Valid         bool        `json:"valid"`
}

// IndicatorResult is the cultured result of a biological indicator.
type IndicatorResult string

const (
	IndicatorNegative IndicatorResult = "negative"
	IndicatorPositive IndicatorResult = "positive"
)

// BiologicalIndicator records the identity, position, culture result and
// interpretation evidence for one biological indicator.
type BiologicalIndicator struct {
	ID         ProbeID         `json:"id"`
	PositionID PositionID      `json:"position_id"`
	Result     IndicatorResult `json:"result"`
	Evidence   string          `json:"evidence"`
}

// FaultClass categorizes a device-call failure for deterministic retry policy.
type FaultClass string

const (
	FaultDisconnect FaultClass = "disconnect"
	FaultTimeout    FaultClass = "timeout"
	FaultMalformed  FaultClass = "malformed"
	FaultDrift      FaultClass = "drift"
)

// DeviceCall records a device invocation, its fault class, retry count and the
// next logical retry time.
type DeviceCall struct {
	OperationID OperationID `json:"operation_id"`
	Fault       FaultClass  `json:"fault"`
	Retries     int         `json:"retries"`
	NextRetryAt LogicalTime `json:"next_retry_at"`
	Receipt     string      `json:"receipt"`
}

// CalculationResult is one append-only lethality/uniformity computation for a
// position over a deterministic input interval.
type CalculationResult struct {
	PositionID        PositionID  `json:"position_id"`
	Accumulated       int64       `json:"accumulated"`
	Lethality         int64       `json:"lethality"`
	MinTemperature    int64       `json:"min_temperature"`
	Uniformity        int64       `json:"uniformity"`
	PressureDeviation int64       `json:"pressure_deviation"`
	InputFrom         LogicalTime `json:"input_from"`
	InputTo           LogicalTime `json:"input_to"`
	AlgorithmVersion  string      `json:"algorithm_version"`
}

// DeviationStatus is the lifecycle state of a deviation case.
type DeviationStatus string

const (
	DeviationOpen   DeviationStatus = "open"
	DeviationClosed DeviationStatus = "closed"
)

// DeviationCase records the source and deterministic propagation of one
// deviation, plus the retest generation it created.
type DeviationCase struct {
	ID               DeviationID     `json:"id"`
	CycleID          CycleID         `json:"cycle_id"`
	Generation       Generation      `json:"generation"`
	Source           string          `json:"source"`
	Propagation      string          `json:"propagation"`
	RetestGeneration Generation      `json:"retest_generation"`
	Status           DeviationStatus `json:"status"`
}

// RetestMember is one normalized, de-duplicated member of a retest set.
type RetestMember struct {
	Device     string     `json:"device"`
	Region     RegionID   `json:"region"`
	Position   PositionID `json:"position"`
	Probe      ProbeID    `json:"probe"`
	Generation Generation `json:"generation"`
}

// ReviewConclusion is a single reviewer's independent conclusion.
type ReviewConclusion string

const (
	ReviewApprove ReviewConclusion = "approve"
	ReviewReject  ReviewConclusion = "reject"
)

// Review records one reviewer's qualification and independent conclusion.
type Review struct {
	ReviewerID string           `json:"reviewer_id"`
	Qualified  bool             `json:"qualified"`
	Conclusion ReviewConclusion `json:"conclusion"`
}

// DecisionKind is the single-writer final outcome.
type DecisionKind string

const (
	DecisionRelease    DecisionKind = "release"
	DecisionQuarantine DecisionKind = "quarantine"
	DecisionVoid       DecisionKind = "void"
)

// FinalDecision is the unique, recoverable terminal outcome for a cycle.
type FinalDecision struct {
	CycleID     CycleID      `json:"cycle_id"`
	Decision    DecisionKind `json:"decision"`
	Credential  string       `json:"credential"`
	OperationID OperationID  `json:"operation_id"`
}

// IdempotencyRecord maps an operation id to its request and response digests,
// so a retry of identical content returns the original result.
type IdempotencyRecord struct {
	OperationID    OperationID `json:"operation_id"`
	RequestDigest  string      `json:"request_digest"`
	ResponseDigest string      `json:"response_digest"`
}
