package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	"github.com/D4NS3U/cbse/scenario-manager/internal/subject"
	"github.com/D4NS3U/cbse/scenario-manager/internal/translatorconfig"
	natsgo "github.com/nats-io/nats.go"
)

const (
	transRequestSubjectTemplateEnv = "SCENARIO_MANAGER_TRANS_REQUEST_SUBJECT_TEMPLATE"
	transReadySubjectTemplateEnv   = "SCENARIO_MANAGER_TRANS_READY_SUBJECT_TEMPLATE"
	transReadyWildcardSubjectEnv   = "SCENARIO_MANAGER_TRANS_READY_WILDCARD_SUBJECT"
	transStreamEnv                 = "SCENARIO_MANAGER_TRANS_STREAM"
	transReadyConsumerEnv          = "SCENARIO_MANAGER_TRANS_READY_CONSUMER"
	transQueueGroupEnv             = "SCENARIO_MANAGER_TRANS_QUEUE_GROUP"
	transAckWaitEnv                = "SCENARIO_MANAGER_TRANS_ACK_WAIT"
	transMaxAckPendingEnv          = "SCENARIO_MANAGER_TRANS_MAX_ACK_PENDING"

	defaultTransRequestSubjectTemplate = "cbse.{project}.trans.request"
	defaultTransReadySubjectTemplate   = "cbse.{project}.trans.{scenario_id}.ready"
	defaultTransReadyWildcardSubject   = "cbse.*.trans.*.ready"
	defaultTransStream                 = "cbse_translator"
	defaultTransReadyConsumer          = "scenario-manager-translator-ready"
	defaultTransQueueGroup             = "scenario-manager-translator-ready"
	defaultTransAckWait                = 2 * time.Minute
	defaultTransMaxAckPending          = 1024
)

// TranslatorComms is the process-scoped NATS/JetStream adapter that satisfies
// the translator communication interfaces used by core.
//
// One instance is expected to be initialized once per Scenario Manager process
// and then reused for many per-scenario handoff calls. It owns broker-level
// details such as stream configuration, durable consumer setup, publish
// confirmation, and ACK/NAK behavior.
type TranslatorComms struct {
	nc  *natsgo.Conn
	js  natsgo.JetStreamContext
	cfg transConfig
}

// transConfig stores translator communication settings parsed from the
// environment and validated into a form ready for the NATS adapter.
type transConfig struct {
	requestSubjectTemplate string
	requestStreamSubject   string
	readySubjectTemplate   string
	readyWildcardSubject   string
	streamName             string
	consumerName           string
	queueGroup             string
	ackWait                time.Duration
	maxAckPending          int
	maxAttempts            int
}

// translationRequestPayload is the JSON body Scenario Manager publishes to the
// Translator request subject after a successful claim.
type translationRequestPayload struct {
	ID                 int             `json:"id"`
	TranslationAttempt int             `json:"translation_attempt"`
	RecipeInfo         json.RawMessage `json:"recipe_info"`
	ConfidenceMetric   *float64        `json:"confidence_metric"`
}

// translatorReadyPayload is the strict JSON body expected from Translator on
// the ready subject once the image is available.
type translatorReadyPayload struct {
	TranslationAttempt int    `json:"translation_attempt"`
	ContainerImage     string `json:"container_image"`
}

// NewTranslatorComms initializes the process-scoped translator communication
// adapter without wiring it into Scenario Manager startup.
//
// The constructor mirrors the existing EDS startup shape: it reads translator
// configuration, validates that the shared NATS connection already exists and
// is connected, creates a JetStream context, ensures the translator stream is
// present, and returns a reusable adapter instance. It does not start the ready
// consumer automatically.
func NewTranslatorComms(ctx context.Context) (*TranslatorComms, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	nc := Connection()
	if nc == nil {
		return nil, errors.New("NATS connection is nil; dependency check has not initialized it")
	}
	if !nc.IsConnected() {
		return nil, errors.New("NATS connection is not ready")
	}

	cfg := loadTranslatorConfig()
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("create translator JetStream context: %w", err)
	}

	if err := ensureTranslatorStream(js, cfg); err != nil {
		return nil, err
	}

	return &TranslatorComms{
		nc:  nc,
		js:  js,
		cfg: cfg,
	}, nil
}

// StartTranslatorComms is an optional convenience wrapper that constructs the
// reusable translator adapter and immediately starts its ready consumer.
//
// The interface-shaped API still lives on *TranslatorComms itself; this helper
// simply packages the common "construct then start consuming" sequence for
// future callers.
func StartTranslatorComms(ctx context.Context, handler communication.TranslatorReadyHandler) (*TranslatorComms, error) {
	comms, err := NewTranslatorComms(ctx)
	if err != nil {
		return nil, err
	}
	if err := comms.StartTranslatorReadyConsumer(ctx, handler); err != nil {
		return nil, err
	}
	return comms, nil
}

// StartTranslatorReadyConsumer attaches the process-scoped durable queue
// consumer used by Scenario Manager to receive Translator ready messages.
//
// The adapter owns subscription setup, transport validation, and ACK/NAK
// mapping. Like the EDS durable consumer, it intentionally does not explicitly
// unsubscribe the JetStream durable subscription on shutdown because doing so
// can delete the shared durable consumer during rolling restarts.
func (c *TranslatorComms) StartTranslatorReadyConsumer(ctx context.Context, handler communication.TranslatorReadyHandler) error {
	if c == nil {
		return fmt.Errorf("translator communication adapter is nil")
	}
	if handler == nil {
		return fmt.Errorf("translator ready handler must not be nil")
	}

	_, err := c.js.QueueSubscribe(c.cfg.readyWildcardSubject, c.cfg.queueGroup, func(msg *natsgo.Msg) {
		handleTranslatorReady(ctx, msg, c.cfg, handler)
	}, natsgo.Durable(c.cfg.consumerName), natsgo.ManualAck(), natsgo.AckWait(c.cfg.ackWait), natsgo.MaxAckPending(c.cfg.maxAckPending))
	if err != nil {
		return fmt.Errorf("subscribe to translator ready subject %q: %w", c.cfg.readyWildcardSubject, err)
	}

	if err := c.nc.Flush(); err != nil {
		return fmt.Errorf("flush translator ready subscription: %w", err)
	}

	log.Printf("Translator communication ready: ready=%q stream=%q durable=%q queue=%q", c.cfg.readyWildcardSubject, c.cfg.streamName, c.cfg.consumerName, c.cfg.queueGroup)
	return nil
}

// PublishTranslationRequest publishes a single claimed translation request and
// returns success only after JetStream confirms acceptance with a PubAck.
//
// The method belongs to the transport adapter layer. It normalizes the raw
// project name from the DB only for broker subject construction, enforces that
// the configured template actually contains a project placeholder, marshals the
// transport JSON body, and treats any missing or unsuccessful PubAck as a
// publish failure.
func (c *TranslatorComms) PublishTranslationRequest(ctx context.Context, scenario coredb.ScenarioForTranslation) error {
	if c == nil {
		return fmt.Errorf("translator communication adapter is nil")
	}
	if scenario.ID <= 0 {
		return fmt.Errorf("scenario ID must be positive")
	}
	if scenario.TranslationAttempt <= 0 {
		return fmt.Errorf("translation attempt must be positive")
	}

	subjectValue, err := requestSubjectForProject(c.cfg, scenario.Project)
	if err != nil {
		return err
	}

	payload := translationRequestPayload{
		ID:                 scenario.ID,
		TranslationAttempt: scenario.TranslationAttempt,
		RecipeInfo:         scenario.RecipeInfo,
		ConfidenceMetric:   scenario.ConfidenceMetric,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal translation request for scenario %d attempt %d: %w", scenario.ID, scenario.TranslationAttempt, err)
	}

	ack, err := c.js.PublishMsg(&natsgo.Msg{
		Subject: subjectValue,
		Data:    data,
	}, natsgo.Context(ctx))
	if err != nil {
		return fmt.Errorf("publish translation request for scenario %d attempt %d: %w", scenario.ID, scenario.TranslationAttempt, err)
	}
	if ack == nil {
		return fmt.Errorf("publish translation request for scenario %d attempt %d returned nil PubAck", scenario.ID, scenario.TranslationAttempt)
	}

	return nil
}

// handleTranslatorReady performs the NATS-side validation and acknowledgement
// mapping for Translator ready messages.
//
// This function owns transport concerns only: subject parsing, strict JSON
// decoding, positivity checks, trimming, and ACK/NAK decisions. Only after
// those checks succeed does it call the core semantic handler. Malformed or
// permanently stale/poison messages are ACKed; only transient semantic failures
// trigger NAK so JetStream can redeliver them.
func handleTranslatorReady(ctx context.Context, msg *natsgo.Msg, cfg transConfig, handler communication.TranslatorReadyHandler) {
	if ctx.Err() != nil {
		log.Printf("Translator ready handling skipped for subject %q because Scenario Manager is shutting down.", msg.Subject)
		nakMsg(msg)
		return
	}

	project, scenarioID, err := parseReadySubject(cfg.readySubjectTemplate, msg.Subject)
	if err != nil {
		log.Printf("Translator ready subject validation failed (subject=%q class=validation): %v", msg.Subject, err)
		ackMsg(msg)
		return
	}
	if strings.TrimSpace(project) == "" {
		log.Printf("Translator ready subject validation failed (subject=%q class=validation): empty project token", msg.Subject)
		ackMsg(msg)
		return
	}
	if scenarioID <= 0 {
		log.Printf("Translator ready subject validation failed (subject=%q class=validation): scenario id must be positive", msg.Subject)
		ackMsg(msg)
		return
	}

	var payload translatorReadyPayload
	if err := decodeStrictJSON(msg.Data, &payload); err != nil {
		log.Printf("Translator ready decode failed (subject=%q class=decode): %v", msg.Subject, err)
		ackMsg(msg)
		return
	}
	if payload.TranslationAttempt <= 0 {
		log.Printf("Translator ready validation failed (subject=%q scenario=%d class=validation): translation attempt must be positive", msg.Subject, scenarioID)
		ackMsg(msg)
		return
	}

	ready := communication.TranslatorReadyMessage{
		Project:            strings.TrimSpace(project),
		ScenarioID:         scenarioID,
		TranslationAttempt: payload.TranslationAttempt,
		ContainerImage:     strings.TrimSpace(payload.ContainerImage),
	}
	result := handler(ctx, ready)
	switch result.Status {
	case communication.TranslatorReadyHandled:
		ackMsg(msg)
	case communication.TranslatorReadyRetry:
		nakMsg(msg)
	default:
		log.Printf("Translator ready handler returned unknown status %q for scenario %d attempt %d; ACKing as poison safeguard.", result.Status, ready.ScenarioID, ready.TranslationAttempt)
		ackMsg(msg)
	}
}

// ensureTranslatorStream guarantees that the translator JetStream stream exists
// and includes both the request and ready subject patterns configured for this
// process.
//
// The helper mirrors the EDS stream setup behavior: create the stream when it
// does not exist, append any missing subjects when it does exist, and never
// remove preexisting subjects that may be used by other deployments.
func ensureTranslatorStream(js natsgo.JetStreamContext, cfg transConfig) error {
	info, err := js.StreamInfo(cfg.streamName)
	if err != nil {
		if !errors.Is(err, natsgo.ErrStreamNotFound) {
			return fmt.Errorf("lookup translator JetStream stream %q: %w", cfg.streamName, err)
		}

		_, err := js.AddStream(&natsgo.StreamConfig{
			Name:      cfg.streamName,
			Subjects:  []string{cfg.requestStreamSubject, cfg.readyWildcardSubject},
			Retention: natsgo.WorkQueuePolicy,
			Storage:   natsgo.FileStorage,
			Discard:   natsgo.DiscardOld,
		})
		if err != nil {
			return fmt.Errorf("create translator JetStream stream %q: %w", cfg.streamName, err)
		}
		return nil
	}

	updated := info.Config
	changed := false
	for _, required := range []string{cfg.requestStreamSubject, cfg.readyWildcardSubject} {
		if !containsSubject(updated.Subjects, required) {
			updated.Subjects = append(updated.Subjects, required)
			changed = true
		}
	}
	if changed {
		if _, err := js.UpdateStream(&updated); err != nil {
			return fmt.Errorf("update translator JetStream stream %q subjects: %w", cfg.streamName, err)
		}
	}

	return nil
}

// loadTranslatorConfig parses translator communication settings from the
// environment and applies the spec defaults for any unset or invalid values.
//
// This helper belongs to the NATS adapter layer because it includes transport
// details such as subjects, durable consumer names, and ACK tuning. Shared
// retry-policy values come from translatorconfig so core and transport stay in
// sync.
func loadTranslatorConfig() transConfig {
	requestTemplate := envOrDefault(transRequestSubjectTemplateEnv, defaultTransRequestSubjectTemplate)
	readyTemplate := envOrDefault(transReadySubjectTemplateEnv, defaultTransReadySubjectTemplate)

	cfg := transConfig{
		requestSubjectTemplate: requestTemplate,
		readySubjectTemplate:   readyTemplate,
		readyWildcardSubject:   envOrDefault(transReadyWildcardSubjectEnv, defaultTransReadyWildcardSubject),
		streamName:             envOrDefault(transStreamEnv, defaultTransStream),
		consumerName:           envOrDefault(transReadyConsumerEnv, defaultTransReadyConsumer),
		queueGroup:             envOrDefault(transQueueGroupEnv, defaultTransQueueGroup),
		ackWait:                defaultTransAckWait,
		maxAckPending:          defaultTransMaxAckPending,
		maxAttempts:            translatorconfig.LoadMaxAttempts(),
	}

	cfg.requestStreamSubject = requestStreamSubjectFromTemplate(cfg.requestSubjectTemplate)
	if cfg.requestStreamSubject == "" {
		log.Printf("Invalid %s value %q; using default %q.", transRequestSubjectTemplateEnv, cfg.requestSubjectTemplate, defaultTransRequestSubjectTemplate)
		cfg.requestSubjectTemplate = defaultTransRequestSubjectTemplate
		cfg.requestStreamSubject = requestStreamSubjectFromTemplate(cfg.requestSubjectTemplate)
	}

	if !validReadyTemplate(cfg.readySubjectTemplate) {
		log.Printf("Invalid %s value %q; using default %q.", transReadySubjectTemplateEnv, cfg.readySubjectTemplate, defaultTransReadySubjectTemplate)
		cfg.readySubjectTemplate = defaultTransReadySubjectTemplate
	}

	if raw := os.Getenv(transAckWaitEnv); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			log.Printf("Invalid %s value %q; using default %s.", transAckWaitEnv, raw, defaultTransAckWait)
		} else {
			cfg.ackWait = parsed
		}
	}

	if raw := os.Getenv(transMaxAckPendingEnv); raw != "" {
		parsed, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			log.Printf("Invalid %s value %q; using default %d.", transMaxAckPendingEnv, raw, defaultTransMaxAckPending)
		case parsed <= 0:
			log.Printf("%s must be positive; using default %d.", transMaxAckPendingEnv, defaultTransMaxAckPending)
		default:
			cfg.maxAckPending = parsed
		}
	}

	return cfg
}

// requestSubjectForProject resolves the concrete request subject for a raw
// project name stored in the database.
//
// The helper trims and normalizes the project only for broker use, then
// replaces the configured project placeholder. Missing placeholders or empty
// normalized project tokens are treated as configuration or input errors rather
// than silently publishing to the wrong subject.
func requestSubjectForProject(cfg transConfig, project string) (string, error) {
	project = strings.TrimSpace(project)
	token := subject.NormalizeToken(project)
	if token == "" {
		return "", fmt.Errorf("project is required for translation request subject")
	}

	switch {
	case strings.Contains(cfg.requestSubjectTemplate, "{project}"):
		return strings.ReplaceAll(cfg.requestSubjectTemplate, "{project}", token), nil
	case strings.Contains(cfg.requestSubjectTemplate, "%s"):
		return strings.ReplaceAll(cfg.requestSubjectTemplate, "%s", token), nil
	default:
		return "", fmt.Errorf("translation request subject template does not include a project placeholder")
	}
}

// requestStreamSubjectFromTemplate derives the request stream subject pattern
// from the configured request subject template by replacing the project
// placeholder with a wildcard.
func requestStreamSubjectFromTemplate(template string) string {
	switch {
	case strings.Contains(template, "{project}"):
		return strings.ReplaceAll(template, "{project}", "*")
	case strings.Contains(template, "%s"):
		return strings.ReplaceAll(template, "%s", "*")
	default:
		return ""
	}
}

// validReadyTemplate verifies that the ready subject template contains exactly
// one project placeholder and exactly one scenario-id placeholder.
func validReadyTemplate(template string) bool {
	projectCount := strings.Count(template, "{project}") + strings.Count(template, "%s")
	return projectCount == 1 && strings.Count(template, "{scenario_id}") == 1
}

// parseReadySubject validates a concrete ready subject against the configured
// template and extracts the project token and numeric scenario id.
//
// Validation is segment-by-segment after splitting on '.', with only the
// project and scenario-id segments treated as dynamic in v1. Any mismatch in
// literal segments, segment count, or scenario-id parsing is a transport-level
// validation failure.
func parseReadySubject(template, actual string) (string, int, error) {
	expectedSegments := strings.Split(template, ".")
	actualSegments := strings.Split(actual, ".")
	if len(expectedSegments) != len(actualSegments) {
		return "", 0, fmt.Errorf("expected %d subject segments, got %d", len(expectedSegments), len(actualSegments))
	}

	var (
		project    string
		scenarioID int
	)
	for i, expected := range expectedSegments {
		actualValue := strings.TrimSpace(actualSegments[i])
		switch expected {
		case "{project}", "%s":
			project = actualValue
		case "{scenario_id}":
			parsed, err := strconv.Atoi(actualValue)
			if err != nil {
				return "", 0, fmt.Errorf("scenario id segment %q is not an integer", actualValue)
			}
			scenarioID = parsed
		default:
			if expected != actualValue {
				return "", 0, fmt.Errorf("subject literal mismatch at segment %d: expected %q, got %q", i, expected, actualValue)
			}
		}
	}

	return project, scenarioID, nil
}

// decodeStrictJSON decodes exactly one JSON object and rejects unknown fields
// and trailing tokens so transport payload shape stays tightly controlled.
func decodeStrictJSON(data []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return err
	}

	var trailing interface{}
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("unexpected trailing JSON content: %w", err)
	}

	return fmt.Errorf("unexpected trailing JSON content")
}
