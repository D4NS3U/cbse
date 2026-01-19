// EDS JetStream intake for scenario batches on the Scenario Manager side.
// This file defines the EDS ↔ SM handshake used to ingest scenarios into the
// Scenario Status table.
package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/D4NS3U/cbse/scenario-manager/internal/coredb"
	natsgo "github.com/nats-io/nats.go"
)

const (
	// edsAvailableSubjectEnv selects the request/reply subject where the EDS
	// announces pending scenarios and the Scenario Manager responds with intake
	// details. This subject is plain NATS (not JetStream).
	edsAvailableSubjectEnv = "SCENARIO_MANAGER_EDS_AVAILABLE_SUBJECT"
	// edsBatchSubjectTemplateEnv selects the JetStream subject template that
	// receives scenario batches from the EDS. Use `{project}` or `%s` as the
	// placeholder for the SimulationExperiment name.
	edsBatchSubjectTemplateEnv = "SCENARIO_MANAGER_EDS_BATCH_SUBJECT_TEMPLATE"
	// edsBatchSubjectEnv selects a fixed JetStream subject for scenario batches.
	// It is treated as a legacy fallback when no template is supplied.
	edsBatchSubjectEnv = "SCENARIO_MANAGER_EDS_BATCH_SUBJECT"
	// edsQueueGroupEnv configures the queue group used by Scenario Manager
	// subscribers so multiple replicas share the batch workload.
	edsQueueGroupEnv = "SCENARIO_MANAGER_EDS_QUEUE_GROUP"
	// edsMaxBatchEnv caps the number of scenarios per batch message. This is a
	// logical guardrail that complements NATS message size limits.
	edsMaxBatchEnv = "SCENARIO_MANAGER_EDS_MAX_BATCH"
	// edsStreamEnv names the JetStream stream that stores scenario batch messages.
	edsStreamEnv = "SCENARIO_MANAGER_EDS_STREAM"
	// edsConsumerEnv names the durable JetStream consumer that Scenario Manager
	// instances use to receive and ACK batch messages.
	edsConsumerEnv = "SCENARIO_MANAGER_EDS_CONSUMER"
	// edsAckWaitEnv overrides the JetStream ACK wait duration. Messages are
	// redelivered if they are not ACKed before this timeout.
	edsAckWaitEnv = "SCENARIO_MANAGER_EDS_ACK_WAIT"
	// edsMaxAckPendingEnv limits how many batch messages can be inflight without
	// ACKs for the durable consumer.
	edsMaxAckPendingEnv = "SCENARIO_MANAGER_EDS_MAX_ACK_PENDING"

	// defaultEDSAvailableSubject is the fallback availability request/reply
	// subject when edsAvailableSubjectEnv is unset.
	defaultEDSAvailableSubject = "cbse.eds.scenarios.available"
	// defaultEDSBatchSubjectTemplate is the fallback template used for per-project
	// batch ingestion when edsBatchSubjectTemplateEnv is unset.
	defaultEDSBatchSubjectTemplate = "cbse.{project}.eds.scenarios"
	// defaultEDSBatchSubject is the legacy fallback JetStream subject used when
	// edsBatchSubjectEnv is set without a template.
	defaultEDSBatchSubject = "cbse.eds.scenarios.batch"
	// defaultEDSQueueGroup is the fallback queue group name used for shared
	// consumption across Scenario Manager replicas.
	defaultEDSQueueGroup = "scenario-manager-eds"
	// defaultEDSMaxBatch is the fallback logical cap on scenarios per batch.
	defaultEDSMaxBatch = 1000
	// defaultEDSStream is the fallback JetStream stream name that stores batch
	// messages when edsStreamEnv is unset.
	defaultEDSStream = "cbse_eds_scenarios"
	// defaultEDSConsumer is the fallback durable consumer name used by Scenario
	// Manager instances to receive batches.
	defaultEDSConsumer = "scenario-manager-eds-consumer"
	// defaultEDSAckWait is the fallback ACK wait timeout before redelivery.
	defaultEDSAckWait = 2 * time.Minute
	// defaultEDSMaxAckPending is the fallback inflight limit for batch messages
	// waiting for ACK.
	defaultEDSMaxAckPending = 1024

	// edsInsertTimeout bounds how long a batch insert can take before failing.
	edsInsertTimeout = 30 * time.Second
)

// EDSAvailabilityNotice is published by the EDS to announce pending scenarios.
// It is expected to be sent as a request so the Scenario Manager can reply
// with an EDSReadyResponse containing the batch subject.
type EDSAvailabilityNotice struct {
	BatchID       string `json:"batch_id,omitempty"`
	Project       string `json:"project,omitempty"`
	ScenarioCount int    `json:"scenario_count,omitempty"`
}

// EDSReadyResponse signals that the Scenario Manager can accept scenario batches.
// The response is intentionally lightweight to reduce NATS payload size.
type EDSReadyResponse struct {
	Status       string `json:"status"`
	Reason       string `json:"reason,omitempty"`
	BatchSubject string `json:"batch_subject,omitempty"`
}

// ScenarioBatch contains the scenarios sent by the EDS to populate the status table.
// The batch is expected to be published to the subject provided in EDSReadyResponse.
type ScenarioBatch struct {
	BatchID   string                  `json:"batch_id,omitempty"`
	Project   string                  `json:"project,omitempty"`
	Scenarios []ScenarioStatusPayload `json:"scenarios"`
}

// ScenarioStatusPayload matches the scenario status schema expected by the core DB.
// It is translated into coredb.ScenarioStatusRecord before persistence.
type ScenarioStatusPayload struct {
	State                string          `json:"state"`
	Priority             int             `json:"priority"`
	NumberOfReps         int             `json:"number_of_reps"`
	NumberOfComputedReps int             `json:"number_of_computed_reps"`
	RecipeInfo           json.RawMessage `json:"recipe_info,omitempty"`
	ContainerImage       string          `json:"container_image,omitempty"`
	ConfidenceMetric     *float64        `json:"confidence_metric,omitempty"`
}

// ScenarioBatchAck reports how many scenarios were persisted from the batch.
// For JetStream deliveries, the Scenario Manager ACKs only after a successful
// insert so failed batches are eligible for redelivery.
type ScenarioBatchAck struct {
	Status   string `json:"status"`
	BatchID  string `json:"batch_id,omitempty"`
	Received int    `json:"received"`
	Inserted int    `json:"inserted"`
	Failed   int    `json:"failed"`
	Reason   string `json:"reason,omitempty"`
}

// edsConfig stores the runtime configuration derived from environment variables.
// It controls subject names, durable consumer tuning, and batch size limits.
type edsConfig struct {
	availableSubject string
	batchSubjectTpl  string
	streamSubject    string
	requireProject   bool
	queueGroup       string
	maxBatch         int
	insertTimeout    time.Duration
	streamName       string
	consumerName     string
	ackWait          time.Duration
	maxAckPending    int
}

// StartEDSComms wires up NATS handlers that coordinate scenario intake from the EDS.
// It ensures a JetStream stream exists for batch deliveries, sets up the
// availability request/reply subject, and attaches a durable queue consumer
// for the batch subject so inserts are ACKed only after persistence succeeds.
// The shared NATS connection from natsconnect.go is reused for all subscriptions.
func StartEDSComms(ctx context.Context) error {
	nc := Connection()
	if nc == nil {
		return errors.New("NATS connection is nil; dependency check has not initialized it")
	}
	if !nc.IsConnected() {
		return errors.New("NATS connection is not ready")
	}

	cfg := loadEDSConfig()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("create JetStream context: %w", err)
	}

	if err := ensureEDSStream(js, cfg); err != nil {
		return err
	}

	availableSub, err := nc.QueueSubscribe(cfg.availableSubject, cfg.queueGroup, func(msg *natsgo.Msg) {
		handleEDSAvailability(ctx, msg, cfg)
	})
	if err != nil {
		return fmt.Errorf("subscribe to EDS availability subject %q: %w", cfg.availableSubject, err)
	}

	batchSub, err := js.QueueSubscribe(cfg.streamSubject, cfg.queueGroup, func(msg *natsgo.Msg) {
		handleEDSScenarioBatch(ctx, msg, cfg)
	}, natsgo.Durable(cfg.consumerName), natsgo.ManualAck(), natsgo.AckWait(cfg.ackWait), natsgo.MaxAckPending(cfg.maxAckPending))
	if err != nil {
		_ = availableSub.Unsubscribe()
		return fmt.Errorf("subscribe to EDS batch subject %q: %w", cfg.streamSubject, err)
	}

	if err := nc.Flush(); err != nil {
		_ = availableSub.Unsubscribe()
		_ = batchSub.Unsubscribe()
		return fmt.Errorf("flush EDS NATS subscriptions: %w", err)
	}

	log.Printf("EDS communication ready: availability=%q batch=%q queue=%q", cfg.availableSubject, cfg.streamSubject, cfg.queueGroup)

	go func() {
		<-ctx.Done()
		_ = availableSub.Unsubscribe()
		_ = batchSub.Unsubscribe()
	}()

	return nil
}

// handleEDSAvailability parses EDS availability notices and responds with
// the batch subject so the EDS can publish scenario batches.
func handleEDSAvailability(ctx context.Context, msg *natsgo.Msg, cfg edsConfig) {
	if ctx.Err() != nil {
		respondJSON(msg, EDSReadyResponse{Status: "error", Reason: "scenario manager shutting down"})
		return
	}

	var notice EDSAvailabilityNotice
	if len(msg.Data) > 0 {
		if err := json.Unmarshal(msg.Data, &notice); err != nil {
			log.Printf("EDS availability payload unmarshal failed: %v", err)
			respondJSON(msg, EDSReadyResponse{Status: "error", Reason: "invalid availability payload"})
			return
		}
	}

	if notice.ScenarioCount > 0 {
		log.Printf("EDS reports %d scenarios ready (batch=%q project=%q).", notice.ScenarioCount, notice.BatchID, notice.Project)
	} else {
		log.Printf("EDS availability notice received (batch=%q project=%q).", notice.BatchID, notice.Project)
	}

	batchSubject, err := batchSubjectForProject(cfg, notice.Project)
	if err != nil {
		respondJSON(msg, EDSReadyResponse{Status: "error", Reason: err.Error()})
		return
	}

	respondJSON(msg, EDSReadyResponse{
		Status:       "ready",
		BatchSubject: batchSubject,
	})
}

// handleEDSScenarioBatch validates the batch payload, translates it into DB
// records, and inserts the scenario status rows. JetStream ACK/NACK is tied
// to persistence success or failure to ensure at-least-once delivery.
func handleEDSScenarioBatch(ctx context.Context, msg *natsgo.Msg, cfg edsConfig) {
	if ctx.Err() != nil {
		respondJSON(msg, ScenarioBatchAck{Status: "error", Reason: "scenario manager shutting down"})
		nakMsg(msg)
		return
	}

	if len(msg.Data) == 0 {
		respondJSON(msg, ScenarioBatchAck{Status: "error", Reason: "empty scenario batch payload"})
		ackMsg(msg)
		return
	}

	var batch ScenarioBatch
	if err := json.Unmarshal(msg.Data, &batch); err != nil {
		log.Printf("EDS scenario batch unmarshal failed: %v", err)
		respondJSON(msg, ScenarioBatchAck{Status: "error", Reason: "invalid scenario batch payload"})
		ackMsg(msg)
		return
	}

	if cfg.maxBatch > 0 && len(batch.Scenarios) > cfg.maxBatch {
		respondJSON(msg, ScenarioBatchAck{
			Status:   "error",
			BatchID:  batch.BatchID,
			Received: len(batch.Scenarios),
			Failed:   len(batch.Scenarios),
			Reason:   "scenario batch exceeds max size",
		})
		ackMsg(msg)
		return
	}

	records := make([]coredb.ScenarioStatusRecord, 0, len(batch.Scenarios))
	for _, scenario := range batch.Scenarios {
		records = append(records, coredb.ScenarioStatusRecord{
			State:                scenario.State,
			Priority:             scenario.Priority,
			NumberOfReps:         scenario.NumberOfReps,
			NumberOfComputedReps: scenario.NumberOfComputedReps,
			RecipeInfo:           scenario.RecipeInfo,
			ContainerImage:       scenario.ContainerImage,
			ConfidenceMetric:     scenario.ConfidenceMetric,
		})
	}

	insertCtx, cancel := context.WithTimeout(ctx, cfg.insertTimeout)
	defer cancel()

	inserted, err := coredb.InsertScenarioStatusBatch(insertCtx, records)
	ack := ScenarioBatchAck{
		Status:   "accepted",
		BatchID:  batch.BatchID,
		Received: len(batch.Scenarios),
		Inserted: inserted,
		Failed:   len(batch.Scenarios) - inserted,
	}
	if err != nil {
		log.Printf("EDS scenario batch insert failed: %v", err)
		ack.Status = "error"
		ack.Reason = err.Error()
		respondJSON(msg, ack)
		nakMsg(msg)
		return
	}

	respondJSON(msg, ack)
	ackMsg(msg)
}

// respondJSON replies to request messages with a JSON payload when a reply
// subject is present.
func respondJSON(msg *natsgo.Msg, payload interface{}) {
	if msg == nil || msg.Reply == "" {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("EDS response marshal failed: %v", err)
		return
	}

	if err := msg.Respond(data); err != nil {
		log.Printf("EDS response publish failed: %v", err)
	}
}

// loadEDSConfig reads environment variables to configure subjects, batch size,
// and JetStream consumer parameters. Defaults are applied for any unset values.
func loadEDSConfig() edsConfig {
	template := os.Getenv(edsBatchSubjectTemplateEnv)
	if template == "" {
		template = defaultEDSBatchSubjectTemplate
	}

	if fixed := os.Getenv(edsBatchSubjectEnv); fixed != "" {
		template = fixed
	}

	cfg := edsConfig{
		availableSubject: envOrDefault(edsAvailableSubjectEnv, defaultEDSAvailableSubject),
		batchSubjectTpl:  template,
		queueGroup:       envOrDefault(edsQueueGroupEnv, defaultEDSQueueGroup),
		maxBatch:         defaultEDSMaxBatch,
		insertTimeout:    edsInsertTimeout,
		streamName:       envOrDefault(edsStreamEnv, defaultEDSStream),
		consumerName:     envOrDefault(edsConsumerEnv, defaultEDSConsumer),
		ackWait:          defaultEDSAckWait,
		maxAckPending:    defaultEDSMaxAckPending,
	}

	cfg.streamSubject, cfg.requireProject = streamSubjectFromTemplate(cfg.batchSubjectTpl)
	if cfg.streamSubject == "" {
		cfg.streamSubject = defaultEDSBatchSubject
		cfg.requireProject = false
	}

	if raw := os.Getenv(edsMaxBatchEnv); raw != "" {
		parsed, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			log.Printf("Invalid %s value %q; using default %d.", edsMaxBatchEnv, raw, defaultEDSMaxBatch)
		case parsed <= 0:
			log.Printf("%s must be positive; using default %d.", edsMaxBatchEnv, defaultEDSMaxBatch)
		default:
			cfg.maxBatch = parsed
		}
	}

	if raw := os.Getenv(edsAckWaitEnv); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			log.Printf("Invalid %s value %q; using default %s.", edsAckWaitEnv, raw, defaultEDSAckWait)
		} else {
			cfg.ackWait = parsed
		}
	}

	if raw := os.Getenv(edsMaxAckPendingEnv); raw != "" {
		parsed, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			log.Printf("Invalid %s value %q; using default %d.", edsMaxAckPendingEnv, raw, defaultEDSMaxAckPending)
		case parsed <= 0:
			log.Printf("%s must be positive; using default %d.", edsMaxAckPendingEnv, defaultEDSMaxAckPending)
		default:
			cfg.maxAckPending = parsed
		}
	}

	return cfg
}

// envOrDefault returns the environment value when present, otherwise fallback.
func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func batchSubjectForProject(cfg edsConfig, project string) (string, error) {
	if cfg.requireProject && project == "" {
		return "", fmt.Errorf("project is required for batch subject")
	}

	if cfg.batchSubjectTpl == "" {
		return "", fmt.Errorf("batch subject template is empty")
	}

	switch {
	case strings.Contains(cfg.batchSubjectTpl, "{project}"):
		return strings.ReplaceAll(cfg.batchSubjectTpl, "{project}", sanitizeSubjectToken(project)), nil
	case strings.Contains(cfg.batchSubjectTpl, "%s"):
		return strings.ReplaceAll(cfg.batchSubjectTpl, "%s", sanitizeSubjectToken(project)), nil
	default:
		if project == "" || !cfg.requireProject {
			return cfg.batchSubjectTpl, nil
		}
		return "", fmt.Errorf("batch subject template does not include a project placeholder")
	}
}

func streamSubjectFromTemplate(template string) (string, bool) {
	switch {
	case strings.Contains(template, "{project}"):
		return strings.ReplaceAll(template, "{project}", "*"), true
	case strings.Contains(template, "%s"):
		return strings.ReplaceAll(template, "%s", "*"), true
	default:
		return template, false
	}
}

func sanitizeSubjectToken(value string) string {
	if value == "" {
		return value
	}

	normalized := strings.ToLower(value)
	out := make([]rune, 0, len(normalized))
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}

	return string(out)
}

// ensureEDSStream guarantees that the JetStream stream exists and is configured
// to retain the batch subject pattern. It creates the stream when absent and
// updates the subject list when needed.
func ensureEDSStream(js natsgo.JetStreamContext, cfg edsConfig) error {
	info, err := js.StreamInfo(cfg.streamName)
	if err != nil {
		if !errors.Is(err, natsgo.ErrStreamNotFound) {
			return fmt.Errorf("lookup JetStream stream %q: %w", cfg.streamName, err)
		}

		_, err := js.AddStream(&natsgo.StreamConfig{
			Name:      cfg.streamName,
			Subjects:  []string{cfg.streamSubject},
			Retention: natsgo.WorkQueuePolicy,
			Storage:   natsgo.FileStorage,
			Discard:   natsgo.DiscardOld,
		})
		if err != nil {
			return fmt.Errorf("create JetStream stream %q: %w", cfg.streamName, err)
		}
		return nil
	}

	if !containsSubject(info.Config.Subjects, cfg.streamSubject) {
		updated := info.Config
		updated.Subjects = append(updated.Subjects, cfg.streamSubject)
		if _, err := js.UpdateStream(&updated); err != nil {
			return fmt.Errorf("update JetStream stream %q subjects: %w", cfg.streamName, err)
		}
	}

	return nil
}

// containsSubject returns true when target exists in the subject slice.
func containsSubject(subjects []string, target string) bool {
	for _, subject := range subjects {
		if subject == target {
			return true
		}
	}
	return false
}

// ackMsg acknowledges a JetStream message so it is removed from the stream.
func ackMsg(msg *natsgo.Msg) {
	if msg == nil {
		return
	}
	if err := msg.Ack(); err != nil {
		log.Printf("EDS JetStream ack failed: %v", err)
	}
}

// nakMsg negatively acknowledges a JetStream message so it can be redelivered.
func nakMsg(msg *natsgo.Msg) {
	if msg == nil {
		return
	}
	if err := msg.Nak(); err != nil {
		log.Printf("EDS JetStream nak failed: %v", err)
	}
}
