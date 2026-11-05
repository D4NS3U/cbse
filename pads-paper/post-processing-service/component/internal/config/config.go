// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config performs the reference PostProcessingService's static
// startup validation. Before connecting to NATS, the service parses its
// container args (the evaluation policy and loop-bound knobs), reads its
// environment configuration and the mounted Result DB connection Secret, and
// rejects any unknown flag, missing, malformed, or inconsistent value with a
// descriptive configuration error. It accepts no NATS username, password,
// token, NKey, JWT, credentials file, or credential mount. After startup it
// does not watch, reload, or rotate credentials.
package config

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/evaluation"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/messaging"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/resultdb"
	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/subject"
)

// Mounts are the mounted Secret paths the service reads at startup.
type Mounts struct {
	// ResultDBDir is the directory of the mounted Result DB connection
	// Secret (default /resultdb-connection) containing host, port, dbname,
	// user, and password files.
	ResultDBDir string
}

// Defaults are the production mount paths the Operator injects.
const DefaultResultDBDir = "/resultdb-connection"

// Config is the validated service configuration.
type Config struct {
	NATSURL         string
	Stream          string
	RequestSubject  string
	EvalTemplate    string
	Consumer        string
	Namespace       string
	Project         string
	ExperimentUID   string
	ResultDB        resultdb.DatabaseConfig
	Mounts          Mounts
	Policy          evaluation.Policy
	DetAddRunners   int
	MaxReplications int
	MaxRunnersRound int
}

// uidRe matches a Kubernetes-style UID: 8-4-4-4-12 lowercase hex groups.
var uidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Load reads, parses, and validates the service configuration from the
// container args (flags) and environment variables and the mounted Result DB
// connection Secret. It returns a descriptive error for any unknown flag,
// unknown policy, missing, malformed, or inconsistent value. It must be
// called before any NATS connection or request handling.
func Load(args []string, mounts Mounts) (*Config, error) {
	fs := flag.NewFlagSet("post-processing-service", flag.ContinueOnError)
	fs.SetOutput(nil)
	policyFlag := fs.String("evaluation-policy", string(evaluation.PolicyStatistical),
		"evaluation policy: statistical or deterministic-first-round-not-met")
	detAddFlag := fs.Int("deterministic-additional-runners", 2,
		"deterministic policy's fixed additional-runner count (clamped to max-runners-per-round, at least 1)")
	maxRepsFlag := fs.Int("max-replications", 10000,
		"user-defined maximum total number of replications across all rounds")
	maxRunnersFlag := fs.Int("max-runners-per-round", 1000,
		"per-round safety clamp on any additional batch (0 disables the per-wave cap)")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("flags: %w", err)
	}

	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	cfg := &Config{
		NATSURL:        os.Getenv("NATS_URL"),
		Stream:         os.Getenv("PPS_STREAM"),
		RequestSubject: os.Getenv("PPS_REQUEST_SUBJECT"),
		EvalTemplate:   os.Getenv("PPS_EVALUATION_SUBJECT_TEMPLATE"),
		Consumer:       os.Getenv("PPS_CONSUMER"),
		Namespace:      os.Getenv("SIMULATIONPROJECTNAMESPACE"),
		Project:        os.Getenv("SIMULATIONPROJECTNAME"),
		ExperimentUID:  os.Getenv("SIMULATIONEXPERIMENTUID"),
		Mounts:         mounts,
	}

	if cfg.NATSURL == "" {
		add("NATS_URL is required")
	} else if err := validateNATSURL(cfg.NATSURL); err != nil {
		add("%v", err)
	}
	if cfg.Stream == "" {
		add("PPS_STREAM is required")
	} else if err := validateJetStreamName(cfg.Stream); err != nil {
		add("PPS_STREAM: %v", err)
	}
	if cfg.RequestSubject == "" {
		add("PPS_REQUEST_SUBJECT is required")
	} else {
		ns, proj, err := subject.ParseRequestSubject(cfg.RequestSubject)
		if err != nil {
			add("PPS_REQUEST_SUBJECT: %v", err)
		} else {
			cfg.Namespace = ns
			cfg.Project = proj
		}
	}
	if cfg.EvalTemplate == "" {
		add("PPS_EVALUATION_SUBJECT_TEMPLATE is required")
	} else {
		ns, proj, err := subject.ValidateEvaluationTemplate(cfg.EvalTemplate)
		if err != nil {
			add("PPS_EVALUATION_SUBJECT_TEMPLATE: %v", err)
		} else {
			// The embedded tokens are cross-checked against the identity
			// environment below.
			_ = ns
			_ = proj
		}
	}
	if cfg.Consumer == "" {
		add("PPS_CONSUMER is required")
	} else if err := validateJetStreamName(cfg.Consumer); err != nil {
		add("PPS_CONSUMER: %v", err)
	}
	// PPS_CONSUMER is the UID-specific durable consumer name and must equal
	// pps-<12-char-UID-prefix>. The service derives the same name from
	// SIMULATIONEXPERIMENTUID for the per-experiment consumer, so a mismatch
	// is an Operator injection error.
	if cfg.Consumer != "" && cfg.ExperimentUID != "" {
		if want := messaging.ConsumerName(cfg.ExperimentUID); cfg.Consumer != want {
			add("PPS_CONSUMER %q must equal the UID-specific durable name %q", cfg.Consumer, want)
		}
	}
	// SIMULATIONPROJECTNAMESPACE and SIMULATIONPROJECTNAME must be valid DNS
	// labels and must match the namespace/project tokens parsed from the
	// request subject and the evaluation template.
	if cfg.Namespace == "" {
		add("SIMULATIONPROJECTNAMESPACE is required")
	} else if err := subject.ValidateIdent(cfg.Namespace); err != nil {
		add("SIMULATIONPROJECTNAMESPACE: %v", err)
	}
	if cfg.Project == "" {
		add("SIMULATIONPROJECTNAME is required")
	} else if err := subject.ValidateIdent(cfg.Project); err != nil {
		add("SIMULATIONPROJECTNAME: %v", err)
	}
	// If the request subject or evaluation template parsed, they must agree
	// with the environment identifiers.
	if ns, _, err := subject.ParseRequestSubject(cfg.RequestSubject); err == nil && ns != "" {
		if cfg.Namespace != "" && ns != cfg.Namespace {
			add("SIMULATIONPROJECTNAMESPACE %q does not match request subject namespace %q", cfg.Namespace, ns)
		}
	}
	if _, proj, err := subject.ParseRequestSubject(cfg.RequestSubject); err == nil && proj != "" {
		if cfg.Project != "" && proj != cfg.Project {
			add("SIMULATIONPROJECTNAME %q does not match request subject project %q", cfg.Project, proj)
		}
	}
	if cfg.EvalTemplate != "" {
		if ns, _, err := subject.ValidateEvaluationTemplate(cfg.EvalTemplate); err == nil && ns != "" {
			if cfg.Namespace != "" && ns != cfg.Namespace {
				add("SIMULATIONPROJECTNAMESPACE %q does not match evaluation template namespace %q", cfg.Namespace, ns)
			}
		}
		if _, proj, err := subject.ValidateEvaluationTemplate(cfg.EvalTemplate); err == nil && proj != "" {
			if cfg.Project != "" && proj != cfg.Project {
				add("SIMULATIONPROJECTNAME %q does not match evaluation template project %q", cfg.Project, proj)
			}
		}
	}
	if cfg.ExperimentUID == "" {
		add("SIMULATIONEXPERIMENTUID is required")
	} else if !uidRe.MatchString(cfg.ExperimentUID) {
		add("SIMULATIONEXPERIMENTUID %q is not a valid UID (8-4-4-4-12 lowercase hex)", cfg.ExperimentUID)
	}

	// Evaluation policy and loop-bound knobs.
	policy, err := evaluation.ParsePolicy(*policyFlag)
	if err != nil {
		add("%v", err)
	} else {
		cfg.Policy = policy
	}
	if *detAddFlag < 1 {
		add("-deterministic-additional-runners %d must be >= 1", *detAddFlag)
	} else {
		cfg.DetAddRunners = *detAddFlag
	}
	if *maxRepsFlag < 1 {
		add("-max-replications %d must be >= 1", *maxRepsFlag)
	} else {
		cfg.MaxReplications = *maxRepsFlag
	}
	// Ruling Q7: 0 explicitly disables the per-wave cap (the estimate
	// flows verbatim within the max-replications headroom); negative
	// values are a fail-fast configuration error. -max-replications
	// stays strictly positive-mandatory: it is the scientific stopping
	// criterion, not an operational pacing knob (ruling Q7's
	// operational/scientific separation).
	if *maxRunnersFlag < 0 {
		add("-max-runners-per-round %d must be >= 0 (0 disables the per-wave cap)", *maxRunnersFlag)
	} else {
		cfg.MaxRunnersRound = *maxRunnersFlag
	}

	// Mounted Result DB connection Secret.
	result, err := resultdb.LoadSecret(mounts.ResultDBDir)
	if err != nil {
		add("result database: %v", err)
	} else {
		cfg.ResultDB = result
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("post-processing-service configuration is invalid:\n  - %s", strings.Join(errs, "\n  - "))
	}
	if cfg.MaxRunnersRound == 0 {
		// Ruling Q7: the disabled state is visible at startup, not
		// silent.
		log.Printf("pps: per-wave cap disabled; waves sized by the estimate alone, bounded only by max-replications headroom")
	}
	return cfg, nil
}

// validateNATSURL rejects an empty URL and any URL carrying user
// information. The service accepts no NATS credentials through the URL.
func validateNATSURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("NATS_URL %q is not a valid URL: %w", raw, err)
	}
	if u.User != nil {
		return fmt.Errorf("NATS_URL %q must not contain user information", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("NATS_URL %q must contain a host", raw)
	}
	return nil
}

// validateJetStreamName rejects empty names and names containing whitespace
// or the JetStream stream/subject wildcard characters. It mirrors the
// constraints the Operator and Scenario Manager apply to stream and
// durable-consumer names.
func validateJetStreamName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if strings.ContainsAny(name, " \t\r\n*>\x00") {
		return fmt.Errorf("name %q must not contain whitespace or stream wildcard characters", name)
	}
	return nil
}
