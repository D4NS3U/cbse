package messaging

import (
	"errors"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

func TestValidateTemplatesAcceptsUnsetAndCanonical(t *testing.T) {
	// No env vars set -> canonical values used, no error.
	if err := ValidateTemplates(func(string) string { return "" }); err != nil {
		t.Fatalf("unset: %v", err)
	}
	// Explicitly canonical -> no error.
	get := func(k string) string {
		switch k {
		case edsAvailableTemplateEnv:
			return CanonicalEDSAvailableTemplate
		case edsBatchTemplateEnv:
			return CanonicalEDSBatchTemplate
		case transRequestTemplateEnv:
			return CanonicalTranslatorRequestTemplate
		case transReadyTemplateEnv:
			return CanonicalTranslatorReadyTemplate
		}
		return ""
	}
	if err := ValidateTemplates(get); err != nil {
		t.Fatalf("canonical: %v", err)
	}
}

func TestValidateTemplatesRejectsNoncanonical(t *testing.T) {
	cases := map[string]string{
		edsAvailableTemplateEnv: "cbse.{namespace}.{project}.eds.scenarios.avail", // wrong suffix
		edsBatchTemplateEnv:     "cbse.{project}.eds.scenarios",                   // missing {namespace}
		transRequestTemplateEnv: "cbse.{namespace}.{project}.trans.req",           // wrong event
		transReadyTemplateEnv:   "cbse.{namespace}.{project}.trans.%s.ready",      // %s not supported
	}
	for env, bad := range cases {
		get := func(k string) string {
			if k == env {
				return bad
			}
			return ""
		}
		err := ValidateTemplates(get)
		if err == nil {
			t.Errorf("ValidateTemplates(%s=%q) succeeded; want error", env, bad)
			continue
		}
		if !errors.Is(err, ErrNoncanonicalConfig) {
			t.Errorf("ValidateTemplates(%s) err = %v; want ErrNoncanonicalConfig", env, err)
		}
	}
}

func TestValidateStreamNames(t *testing.T) {
	if err := ValidateStreamNames(func(string) string { return "" }); err != nil {
		t.Fatalf("unset: %v", err)
	}
	get := func(k string) string {
		switch k {
		case edsStreamNameEnv:
			return EDSStreamName
		case translatorStreamNameEnv:
			return TranslatorStreamName
		}
		return ""
	}
	if err := ValidateStreamNames(get); err != nil {
		t.Fatalf("canonical: %v", err)
	}
	getBad := func(k string) string {
		if k == edsStreamNameEnv {
			return "legacy_eds_stream"
		}
		return ""
	}
	if err := ValidateStreamNames(getBad); !errors.Is(err, ErrNoncanonicalConfig) {
		t.Fatalf("bad eds stream: err = %v; want ErrNoncanonicalConfig", err)
	}
}

func TestStreamConfigs(t *testing.T) {
	eds := EDSStreamConfig()
	if eds.Name != EDSStreamName || eds.Retention != natsgo.WorkQueuePolicy || eds.Storage != natsgo.FileStorage || eds.Discard != natsgo.DiscardOld {
		t.Fatalf("eds stream config = %+v", eds)
	}
	if len(eds.Subjects) != 1 || eds.Subjects[0] != "cbse.*.*.eds.scenarios" {
		t.Fatalf("eds subjects = %v", eds.Subjects)
	}
	tr := TranslatorStreamConfig()
	if tr.Name != TranslatorStreamName || tr.Retention != natsgo.WorkQueuePolicy || tr.Storage != natsgo.FileStorage {
		t.Fatalf("translator stream config = %+v", tr)
	}
	if len(tr.Subjects) != 2 || tr.Subjects[0] != "cbse.*.*.trans.request" || tr.Subjects[1] != "cbse.*.*.trans.*.ready" {
		t.Fatalf("translator subjects = %v", tr.Subjects)
	}
}

func TestSMConsumerConfigs(t *testing.T) {
	eds := EDSConsumerConfig()
	if eds.Durable != EDSConsumerName || eds.DeliverGroup != "scenario-manager-eds" {
		t.Fatalf("eds consumer = %+v", eds)
	}
	if eds.FilterSubject != "cbse.*.*.eds.scenarios" || eds.AckPolicy != natsgo.AckExplicitPolicy || eds.DeliverPolicy != natsgo.DeliverAllPolicy {
		t.Fatalf("eds consumer settings = %+v", eds)
	}
	if eds.AckWait != 2*time.Minute || eds.MaxAckPending != 1024 || eds.MaxDeliver != -1 {
		t.Fatalf("eds consumer tuning = %+v", eds)
	}
	ready := TranslatorReadyConsumerConfig()
	if ready.Durable != TranslatorReadyConsumerName || ready.DeliverGroup != "scenario-manager-translator-ready" {
		t.Fatalf("ready consumer = %+v", ready)
	}
	if ready.FilterSubject != "cbse.*.*.trans.*.ready" || ready.MaxAckPending != 1024 || ready.MaxDeliver != -1 {
		t.Fatalf("ready consumer settings = %+v", ready)
	}
	if got := SMConsumers(); len(got) != 2 || got[0].Stream != EDSStreamName || got[1].Stream != TranslatorStreamName {
		t.Fatalf("SMConsumers = %+v", got)
	}
}

func TestTranslatorConsumerName(t *testing.T) {
	uid := "A1B2C3D4-E5F6-7890-ABCD-EF1234567890"
	// lowercase + hyphen-stripped + first 12 chars -> a1b2c3d4e5f6
	want := "translator-a1b2c3d4e5f6"
	if got := TranslatorConsumerName(uid); got != want {
		t.Fatalf("TranslatorConsumerName(%q) = %q; want %q", uid, got, want)
	}
	// Already-lowercase, hyphenless UID.
	if got := TranslatorConsumerName("a1b2c3d4e5f67890abcdef1234567890"); got != "translator-a1b2c3d4e5f6" {
		t.Fatalf("hyphenless = %q", got)
	}
}

func TestTranslatorConsumerConfig(t *testing.T) {
	cfg := TranslatorConsumerConfig("A1B2C3D4-E5F6-7890-ABCD-EF1234567890", "default", "smoke")
	if cfg.Durable != "translator-a1b2c3d4e5f6" {
		t.Fatalf("durable = %q", cfg.Durable)
	}
	if cfg.FilterSubject != "cbse.default.smoke.trans.request" {
		t.Fatalf("filter = %q", cfg.FilterSubject)
	}
	if cfg.AckPolicy != natsgo.AckExplicitPolicy || cfg.DeliverPolicy != natsgo.DeliverAllPolicy {
		t.Fatalf("policies = %+v", cfg)
	}
	if cfg.AckWait != 2*time.Minute || cfg.MaxAckPending != 1 || cfg.MaxDeliver != -1 {
		t.Fatalf("tuning = %+v", cfg)
	}
	wantMeta := map[string]string{
		"experiment.cbse.terministic.de/managed-by":     "translator",
		"experiment.cbse.terministic.de/experiment-uid": "A1B2C3D4-E5F6-7890-ABCD-EF1234567890",
		"experiment.cbse.terministic.de/namespace":      "default",
		"experiment.cbse.terministic.de/project":        "smoke",
	}
	for k, v := range wantMeta {
		if cfg.Metadata[k] != v {
			t.Fatalf("metadata %q = %q; want %q", k, cfg.Metadata[k], v)
		}
	}
}
