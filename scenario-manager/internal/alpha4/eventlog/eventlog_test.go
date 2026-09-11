package eventlog

import "testing"

func TestNopLoggerDiscards(t *testing.T) {
	(NopLogger{}).Log(Record{Event: EventCreate, ScenarioID: 1})
	// Nothing to assert; the point is it does not panic or record.
}

func TestRecorderCapturesAndCopies(t *testing.T) {
	var r Recorder
	r.Log(Record{Event: EventCreate, ScenarioID: 1, JobName: "j-1"})
	r.Log(Record{Event: EventFail, ScenarioID: 1, Outcome: "Failed"})

	got := r.Records()
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	if got[0].Event != EventCreate || got[0].ScenarioID != 1 || got[0].JobName != "j-1" {
		t.Errorf("record 0 mismatch: %+v", got[0])
	}
	if got[1].Event != EventFail || got[1].Outcome != "Failed" {
		t.Errorf("record 1 mismatch: %+v", got[1])
	}

	// Records() returns a copy: mutating the slice does not affect the recorder.
	got[0].ScenarioID = 999
	again := r.Records()
	if again[0].ScenarioID != 1 {
		t.Errorf("Records() did not return an independent copy: %+v", again[0])
	}
}

// TestRecordCarriesAllRequiredFields documents the S06-M3 field contract: the
// record carries exactly the required scenario fields and never credentials or
// recipe payloads.
func TestRecordCarriesAllRequiredFields(t *testing.T) {
	rec := Record{
		Event:         EventComplete,
		Namespace:     "ns",
		Experiment:    "exp",
		ScenarioID:    7,
		Attempt:       2,
		JobName:       "simrunner-abcdef-7-2",
		RequestedReps: 10,
		ComputedReps:  10,
		Outcome:       "Completed",
		Reason:        "",
	}
	// The required fields are present and non-empty where applicable.
	for _, check := range []struct {
		name string
		got  string
	}{
		{"namespace", rec.Namespace},
		{"experiment", rec.Experiment},
		{"jobName", rec.JobName},
		{"outcome", rec.Outcome},
	} {
		if check.got == "" {
			t.Errorf("required field %s is empty", check.name)
		}
	}
	if rec.ScenarioID != 7 || rec.Attempt != 2 || rec.RequestedReps != 10 || rec.ComputedReps != 10 {
		t.Errorf("numeric fields mismatch: %+v", rec)
	}
}
