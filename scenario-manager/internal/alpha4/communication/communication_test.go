package communication

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/D4NS3U/cbse/scenario-manager/internal/alpha4/subject"
)

func TestScenarioBatchDecodesLegacyFixture(t *testing.T) {
	// Existing payload fixtures must continue to decode without namespace or
	// UID fields.
	raw := `{"batch_id":"b1","project":"smoke","scenarios":[{"priority":1,"number_of_reps":10,"recipe_info":{"k":"v"}}]}`
	var batch ScenarioBatch
	if err := json.Unmarshal([]byte(raw), &batch); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if batch.Project != "smoke" || len(batch.Scenarios) != 1 || batch.Scenarios[0].NumberOfReps != 10 {
		t.Fatalf("decoded batch = %+v", batch)
	}
	// Non-JSON identity fields are zero after decode.
	if batch.ProjectNamespace != "" || batch.ProjectName != "" {
		t.Fatalf("non-JSON identity fields populated from JSON: %+v", batch)
	}
}

func TestValidateBatchIdentity(t *testing.T) {
	id := subject.Identity{Namespace: "default", Project: "smoke"}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: "smoke"}, id); err != nil {
		t.Fatalf("matching: %v", err)
	}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: ""}, id); !errors.Is(err, ErrBatchValidation) {
		t.Fatalf("empty project: %v", err)
	}
	if err := ValidateBatchIdentity(ScenarioBatch{Project: "other"}, id); !errors.Is(err, ErrBatchValidation) {
		t.Fatalf("mismatch: %v", err)
	}
}

func TestValidateBatchReps(t *testing.T) {
	valid := []ScenarioRecord{{NumberOfReps: 1}, {NumberOfReps: 100000}}
	if err := ValidateBatchReps(valid); err != nil {
		t.Fatalf("valid: %v", err)
	}
	invalid := []struct {
		recs []ScenarioRecord
		ok   bool
	}{
		{recs: []ScenarioRecord{{NumberOfReps: 0}}, ok: false},
		{recs: []ScenarioRecord{{NumberOfReps: 100001}}, ok: false},
		{recs: []ScenarioRecord{{NumberOfReps: 1}, {NumberOfReps: -1}}, ok: false},
		{recs: []ScenarioRecord{}, ok: true},
	}
	for i, c := range invalid {
		err := ValidateBatchReps(c.recs)
		if c.ok && err != nil {
			t.Errorf("case %d: unexpected error %v", i, err)
		}
		if !c.ok && !errors.Is(err, ErrBatchValidation) {
			t.Errorf("case %d: err = %v; want ErrBatchValidation", i, err)
		}
	}
}
