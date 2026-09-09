package wire

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeRequestFourFields(t *testing.T) {
	cm := 0.95
	data, _ := json.Marshal(map[string]any{
		"id":                  42,
		"translation_attempt": 3,
		"recipe_info":         map[string]int{"parameterset_id": 7},
		"confidence_metric":   cm,
	})
	req, err := DecodeRequest(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.ID != 42 || req.TranslationAttempt != 3 {
		t.Fatalf("id/attempt = %d/%d", req.ID, req.TranslationAttempt)
	}
	if string(req.RecipeInfo) == "" {
		t.Fatal("recipe_info empty")
	}
	if req.ConfidenceMetric == nil || *req.ConfidenceMetric != cm {
		t.Fatalf("confidence = %v", req.ConfidenceMetric)
	}
}

func TestDecodeRequestNullConfidenceMetric(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"id": 1, "translation_attempt": 1, "recipe_info": map[string]int{"parameterset_id": 1}, "confidence_metric": nil,
	})
	req, err := DecodeRequest(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.ConfidenceMetric != nil {
		t.Fatalf("confidence = %v, want nil", req.ConfidenceMetric)
	}
}

func TestDecodeRequestRejectsUnknownField(t *testing.T) {
	data, _ := json.Marshal(map[string]any{
		"id": 1, "translation_attempt": 1, "recipe_info": map[string]int{"parameterset_id": 1}, "extra": 1,
	})
	if _, err := DecodeRequest(data); err == nil {
		t.Fatal("unknown field must be rejected")
	}
}

func TestDecodeRequestRejectsTrailingContent(t *testing.T) {
	data := []byte(`{"id":1,"translation_attempt":1,"recipe_info":{"parameterset_id":1},"confidence_metric":1.0} extra`)
	if _, err := DecodeRequest(data); err == nil {
		t.Fatal("trailing content must be rejected")
	}
}

func TestDecodeRequestRejectsMalformed(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(""), []byte("{"), []byte("[]"), []byte("42")} {
		if _, err := DecodeRequest(data); err == nil {
			t.Fatalf("malformed %q must be rejected", data)
		}
	}
}

func TestHasUsableIdentity(t *testing.T) {
	cases := []struct {
		id, attempt int
		want        bool
	}{
		{1, 1, true}, {42, 3, true},
		{0, 1, false}, {-1, 1, false}, {1, 0, false}, {1, -1, false},
	}
	for _, c := range cases {
		r := &Request{ID: c.id, TranslationAttempt: c.attempt}
		if r.HasUsableIdentity() != c.want {
			t.Fatalf("id=%d attempt=%d: got %v want %v", c.id, c.attempt, r.HasUsableIdentity(), c.want)
		}
	}
	if (*Request)(nil).HasUsableIdentity() {
		t.Fatal("nil request must not have usable identity")
	}
}

func TestEncodeReady(t *testing.T) {
	data, err := EncodeReady(3, "registry.example.com/test:tag@sha256:abc")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var p ReadyPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p.TranslationAttempt != 3 || p.ContainerImage != "registry.example.com/test:tag@sha256:abc" {
		t.Fatalf("payload = %+v", p)
	}
	// Empty-image (empty-failure) ready.
	data2, err := EncodeReady(3, "")
	if err != nil {
		t.Fatalf("encode empty: %v", err)
	}
	if err := json.Unmarshal(data2, &p); err != nil {
		t.Fatal(err)
	}
	if p.ContainerImage != "" {
		t.Fatalf("empty-image container = %q, want empty", p.ContainerImage)
	}
}

func TestEncodeReadyRejectsNonPositiveAttempt(t *testing.T) {
	for _, a := range []int{0, -1} {
		if _, err := EncodeReady(a, "img"); err == nil {
			t.Fatalf("attempt %d must be rejected", a)
		}
	}
}

// Ensure the error types are comparable for sentinel checks if needed.
var _ = errors.Is
