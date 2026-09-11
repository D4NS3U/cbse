// Package eventlog provides the lightweight Slice 06 scenario-observability
// logger. Per S06-M3, Scenario Manager emits exactly two kinds of records per
// scenario: one Job creation or adoption record (when the runner-start
// reconciler creates or confirms the deterministic Job) and one terminal
// scenario-outcome record (when the scenario reaches a terminal state — Failed
// at runner start, or Completed or Failed during observation). SM does not log
// every successful repetition or every unchanged observation poll, and no
// record carries credentials or recipe payloads.
//
// The Logger interface is resource-neutral so the runner-start and observation
// schedulers can emit records without importing Kubernetes types. A NopLogger
// discards records; tests use a capturing fake.
package eventlog

import "sync"

// Event is the kind of scenario-observability record.
type Event string

const (
	// EventCreate is emitted when the runner-start reconciler creates the
	// deterministic Job.
	EventCreate Event = "create"
	// EventAdopt is emitted when the runner-start reconciler confirms an
	// already-existing deterministic Job (AlreadyExists recovery).
	EventAdopt Event = "adopt"
	// EventComplete is emitted when observation observes Kubernetes
	// Complete=True and the scenario advances to PostProcessing.
	EventComplete Event = "complete"
	// EventFail is emitted when the scenario reaches a terminal Failed state,
	// whether at runner start (Collision/Forbidden/ProjectionInvalid) or
	// during observation (Failed/Collision/Forbidden).
	EventFail Event = "fail"
)

// Record is one lightweight scenario-observability record. It carries exactly
// the required scenario fields and never credentials or recipe payloads. The
// ComputedReps field is zero for creation/adoption records and the final
// monotonic count for terminal records.
type Record struct {
	Event         Event
	Namespace     string // experiment namespace
	Experiment    string // experiment name
	ScenarioID    int
	Attempt       int // translation attempt
	JobName       string
	RequestedReps int
	ComputedReps  int
	Outcome       string // stable outcome name
	Reason        string // short reason; must not contain credential material
}

// Logger emits scenario-observability records.
type Logger interface {
	Log(rec Record)
}

// NopLogger discards all records.
type NopLogger struct{}

// Log implements Logger by discarding the record.
func (NopLogger) Log(Record) {}

// Recorder is an in-memory Logger for tests. It is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	records []Record
}

// Log appends the record.
func (r *Recorder) Log(rec Record) {
	r.mu.Lock()
	r.records = append(r.records, rec)
	r.mu.Unlock()
}

// Records returns a copy of the captured records.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Record, len(r.records))
	copy(out, r.records)
	return out
}
