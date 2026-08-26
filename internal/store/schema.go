package store

// schema is the complete relational schema for the embedded SQL database. It
// persists every append-only fact in the workflow: plans, probe lineage,
// bindings, leases, cycle events and snapshots, samples, indicators, device
// calls, calculation results, deviations, retest members, reviews, the single
// final decision and idempotency records.
//
// Uniqueness constraints are the single-writer barriers described in the
// project document: a (cycle, generation, sequence) event key, a per-position
// sample key, one final decision per cycle, and one idempotency record per
// operation id.
const schema = `
CREATE TABLE IF NOT EXISTS plans (
    id                    TEXT PRIMARY KEY,
    generation            INTEGER NOT NULL,
    structure_digest      TEXT NOT NULL,
    load_digest           TEXT NOT NULL,
    exposure_min_temp     INTEGER NOT NULL,
    exposure_min_pressure INTEGER NOT NULL,
    exposure_max_pressure INTEGER NOT NULL,
    exposure_min_duration INTEGER NOT NULL,
    sampling_interval     INTEGER NOT NULL,
    lethality_threshold   INTEGER NOT NULL,
    locked_at             INTEGER NOT NULL,
    status                TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plan_regions (
    plan_id   TEXT NOT NULL,
    region_id TEXT NOT NULL,
    name      TEXT NOT NULL,
    kind      TEXT NOT NULL,
    PRIMARY KEY (plan_id, region_id)
);

CREATE TABLE IF NOT EXISTS plan_positions (
    plan_id     TEXT NOT NULL,
    position_id TEXT NOT NULL,
    region_id   TEXT NOT NULL,
    load_layer  INTEGER NOT NULL,
    PRIMARY KEY (plan_id, position_id)
);

CREATE TABLE IF NOT EXISTS plan_probe_summaries (
    plan_id     TEXT NOT NULL,
    probe_id    TEXT NOT NULL,
    position_id TEXT NOT NULL,
    certificate TEXT NOT NULL,
    PRIMARY KEY (plan_id, probe_id)
);

CREATE TABLE IF NOT EXISTS probes (
    id                TEXT PRIMARY KEY,
    type              TEXT NOT NULL,
    range_min         INTEGER NOT NULL,
    range_max         INTEGER NOT NULL,
    certificate       TEXT NOT NULL,
    calibration_batch TEXT NOT NULL,
    valid_from        INTEGER NOT NULL,
    valid_until       INTEGER NOT NULL,
    status            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS bindings (
    probe_id    TEXT NOT NULL,
    position_id TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    valid_from  INTEGER NOT NULL,
    valid_until INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS leases (
    resource_id  TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    generation   INTEGER NOT NULL,
    token        TEXT PRIMARY KEY,
    valid_from   INTEGER NOT NULL,
    valid_until  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cycle_events (
    cycle_id     TEXT NOT NULL,
    generation   INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    phase        TEXT NOT NULL,
    logical_time INTEGER NOT NULL,
    operation_id TEXT NOT NULL,
    input_digest TEXT NOT NULL,
    audit        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (cycle_id, generation, seq)
);

CREATE TABLE IF NOT EXISTS cycle_snapshots (
    cycle_id      TEXT PRIMARY KEY,
    validation_id TEXT NOT NULL DEFAULT '',
    generation    INTEGER NOT NULL,
    cursor        INTEGER NOT NULL,
    status        TEXT NOT NULL,
    checksum      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS samples (
    cycle_id       TEXT NOT NULL,
    generation     INTEGER NOT NULL,
    probe_id       TEXT NOT NULL,
    seq            INTEGER NOT NULL,
    logical_time   INTEGER NOT NULL,
    reading        INTEGER NOT NULL,
    device_receipt TEXT NOT NULL,
    valid          INTEGER NOT NULL,
    PRIMARY KEY (cycle_id, generation, probe_id, seq)
);

CREATE TABLE IF NOT EXISTS biological_indicators (
    cycle_id    TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    probe_id    TEXT NOT NULL,
    position_id TEXT NOT NULL,
    result      TEXT NOT NULL,
    evidence    TEXT NOT NULL,
    PRIMARY KEY (cycle_id, generation, probe_id)
);

CREATE TABLE IF NOT EXISTS device_calls (
    operation_id  TEXT PRIMARY KEY,
    fault         TEXT NOT NULL,
    retries       INTEGER NOT NULL,
    next_retry_at INTEGER NOT NULL,
    receipt       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS calculation_results (
    cycle_id           TEXT NOT NULL,
    generation         INTEGER NOT NULL,
    position_id        TEXT NOT NULL,
    accumulated        INTEGER NOT NULL,
    lethality          INTEGER NOT NULL,
    min_temperature    INTEGER NOT NULL,
    uniformity         INTEGER NOT NULL,
    pressure_deviation INTEGER NOT NULL,
    input_from         INTEGER NOT NULL,
    input_to           INTEGER NOT NULL,
    algorithm_version  TEXT NOT NULL,
    PRIMARY KEY (cycle_id, generation, position_id)
);

CREATE TABLE IF NOT EXISTS deviation_cases (
    id                TEXT PRIMARY KEY,
    cycle_id          TEXT NOT NULL,
    generation        INTEGER NOT NULL,
    source            TEXT NOT NULL,
    propagation       TEXT NOT NULL,
    retest_generation INTEGER NOT NULL,
    status            TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_deviation_propagation
    ON deviation_cases (cycle_id, propagation);

CREATE TABLE IF NOT EXISTS retest_members (
    cycle_id          TEXT NOT NULL,
    retest_generation INTEGER NOT NULL,
    device            TEXT NOT NULL,
    region            TEXT NOT NULL,
    position          TEXT NOT NULL,
    probe             TEXT NOT NULL,
    generation        INTEGER NOT NULL,
    PRIMARY KEY (cycle_id, retest_generation, device, region, position, probe, generation)
);

CREATE TABLE IF NOT EXISTS reviews (
    cycle_id    TEXT NOT NULL,
    generation  INTEGER NOT NULL,
    reviewer_id TEXT NOT NULL,
    qualified   INTEGER NOT NULL,
    conclusion  TEXT NOT NULL,
    PRIMARY KEY (cycle_id, generation, reviewer_id)
);

CREATE TABLE IF NOT EXISTS final_decisions (
    cycle_id     TEXT PRIMARY KEY,
    decision     TEXT NOT NULL,
    credential   TEXT NOT NULL,
    operation_id TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS idempotency (
    operation_id    TEXT PRIMARY KEY,
    request_digest  TEXT NOT NULL,
    response_digest TEXT NOT NULL
);
`
