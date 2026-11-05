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

// Package config performs the reference ExperimentalDesignService's static
// startup validation. Before serving, the service parses its container args
// (the listen address), reads its environment configuration and the mounted
// Detail DB connection Secret, and rejects any unknown flag, missing,
// malformed, or inconsistent value with a descriptive configuration error. It
// accepts no NATS username, password, token, NKey, JWT, credentials file, or
// credential mount. The batch subject is never part of the configuration: it
// arrives from the Scenario Manager's availability reply at runtime. After
// startup it does not watch, reload, or rotate credentials.
package config

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Mounts are the mounted Secret paths the service reads at startup.
type Mounts struct {
	// DetailDBDir is the directory of the mounted Detail DB connection
	// Secret (default /detaildb-connection) containing host, port, dbname,
	// user, and password files.
	DetailDBDir string
}

// Defaults are the production mount paths the Operator injects.
const DefaultDetailDBDir = "/detaildb-connection"

// Config is the validated service configuration.
type Config struct {
	NATSURL             string
	AvailabilitySubject string
	// Listen is the HTTP listen address (container arg, default :8082).
	Listen        string
	Namespace     string
	Project       string
	ExperimentUID string
	DetailDB      DatabaseConfig
	Mounts        Mounts
}

// DatabaseConfig is the exact set of PostgreSQL connection fields the EDS
// reads from the mounted Detail DB connection Secret: host, port, dbname,
// user, and password. The EDS always sets sslmode=disable when connecting;
// the Secret carries no sslmode field (the same five-field contract the
// Translator's dbconfig validates).
type DatabaseConfig struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
}

// Validate reports whether all five fields are present and the port is in the
// valid TCP range. The EDS calls this at startup before serving and rejects
// missing or malformed values.
func (c DatabaseConfig) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("detail database connection field host is empty")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("detail database connection field port %d is outside 1..65535", c.Port)
	}
	if c.DBName == "" {
		return fmt.Errorf("detail database connection field dbname is empty")
	}
	if c.User == "" {
		return fmt.Errorf("detail database connection field user is empty")
	}
	if c.Password == "" {
		return fmt.Errorf("detail database connection field password is empty")
	}
	return nil
}

// LoadSecret reads the five connection files from dir (the mounted Detail DB
// connection Secret) and returns the validated DatabaseConfig. An empty file
// or a missing file is an error.
func LoadSecret(dir string) (DatabaseConfig, error) {
	var c DatabaseConfig
	if dir == "" {
		return c, fmt.Errorf("detail database connection directory is empty")
	}
	host, err := readKey(dir, "host")
	if err != nil {
		return c, err
	}
	portStr, err := readKey(dir, "port")
	if err != nil {
		return c, err
	}
	dbname, err := readKey(dir, "dbname")
	if err != nil {
		return c, err
	}
	user, err := readKey(dir, "user")
	if err != nil {
		return c, err
	}
	password, err := readKey(dir, "password")
	if err != nil {
		return c, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil {
		return c, fmt.Errorf("detail database port %q is not an integer", portStr)
	}
	c = DatabaseConfig{Host: host, Port: port, DBName: dbname, User: user, Password: password}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// readKey reads and trims a single connection file. An empty value is an
// error.
func readKey(dir, key string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, key))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("%s is empty", key)
	}
	return v, nil
}

// uidRe matches a Kubernetes-style UID: 8-4-4-4-12 lowercase hex groups.
var uidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// identRe repeats the CRD admission rule: a lowercase DNS label of 1-63
// characters.
var identRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Load reads, parses, and validates the service configuration from the
// container args and environment variables and the mounted Detail DB
// connection Secret. It returns a descriptive error for any unknown flag,
// missing, malformed, or inconsistent value. It must be called before any
// NATS connection or HTTP serving.
func Load(args []string, mounts Mounts) (*Config, error) {
	fs := flag.NewFlagSet("experimental-design-service", flag.ContinueOnError)
	fs.SetOutput(nil)
	listenFlag := fs.String("listen", ":8082", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("flags: %w", err)
	}

	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	cfg := &Config{
		NATSURL:             os.Getenv("NATS_URL"),
		AvailabilitySubject: os.Getenv("EDS_AVAILABILITY_SUBJECT"),
		Listen:              *listenFlag,
		Namespace:           os.Getenv("SIMULATIONPROJECTNAMESPACE"),
		Project:             os.Getenv("SIMULATIONPROJECTNAME"),
		ExperimentUID:       os.Getenv("SIMULATIONEXPERIMENTUID"),
		Mounts:              mounts,
	}

	if cfg.NATSURL == "" {
		add("NATS_URL is required")
	} else if err := validateNATSURL(cfg.NATSURL); err != nil {
		add("%v", err)
	}
	if cfg.Namespace == "" {
		add("SIMULATIONPROJECTNAMESPACE is required")
	} else if err := validateIdent(cfg.Namespace); err != nil {
		add("SIMULATIONPROJECTNAMESPACE: %v", err)
	}
	if cfg.Project == "" {
		add("SIMULATIONPROJECTNAME is required")
	} else if err := validateIdent(cfg.Project); err != nil {
		add("SIMULATIONPROJECTNAME: %v", err)
	}
	if cfg.ExperimentUID == "" {
		add("SIMULATIONEXPERIMENTUID is required")
	} else if !uidRe.MatchString(cfg.ExperimentUID) {
		add("SIMULATIONEXPERIMENTUID %q is not a valid UID (8-4-4-4-12 lowercase hex)", cfg.ExperimentUID)
	}
	// The availability subject is operator-derived from the identity and must
	// be exactly the canonical per-project subject; a free-form subject would
	// point the handshake at the wrong project.
	if cfg.AvailabilitySubject == "" {
		add("EDS_AVAILABILITY_SUBJECT is required")
	} else if want := CanonicalAvailabilitySubject(cfg.Namespace, cfg.Project); cfg.AvailabilitySubject != want {
		add("EDS_AVAILABILITY_SUBJECT %q must equal the canonical subject %q", cfg.AvailabilitySubject, want)
	}
	if cfg.Listen == "" {
		add("-listen must not be empty")
	}

	// Mounted Detail DB connection Secret.
	detail, err := LoadSecret(mounts.DetailDBDir)
	if err != nil {
		add("detail database: %v", err)
	} else {
		cfg.DetailDB = detail
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("experimental-design-service configuration is invalid:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return cfg, nil
}

// CanonicalAvailabilitySubject is the per-project availability subject the
// Operator derives from the identity: cbse.<namespace>.<project>.eds.scenarios.available.
func CanonicalAvailabilitySubject(namespace, project string) string {
	return "cbse." + namespace + "." + project + ".eds.scenarios.available"
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

// validateIdent rejects an empty or malformed DNS label (lowercase, 1-63
// characters, RFC 1123).
func validateIdent(s string) error {
	if len(s) < 1 || len(s) > 63 || !identRe.MatchString(s) {
		return fmt.Errorf("%q is not a valid DNS label", s)
	}
	return nil
}
