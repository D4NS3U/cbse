// Package config performs the reference Translator framework's static startup
// validation. Before connecting to NATS, the framework reads its environment
// configuration and the mounted Detail DB, Result DB, and registry-auth
// Secrets and rejects any missing, malformed, or inconsistent value with a
// descriptive configuration error. It accepts no NATS username, password,
// token, NKey, JWT, credentials file, or credential mount. After startup it
// does not watch, reload, or rotate credentials.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/imageref"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/subject"
)

// Mounts are the mounted Secret paths the framework reads at startup.
type Mounts struct {
	// DetailDBDir is the directory of the mounted Detail DB connection Secret
	// (default /detaildb-connection) containing host, port, dbname, user, and
	// password files.
	DetailDBDir string
	// ResultDBDir is the directory of the mounted Result DB connection Secret
	// (default /resultdb-connection) containing host, port, dbname, user, and
	// password files.
	ResultDBDir string
	// RegistryAuth is the mounted Docker config.json path (default
	// /registry-auth/config.json).
	RegistryAuth string
}

// Defaults are the production mount paths the Operator injects.
const (
	DefaultDetailDBDir  = "/detaildb-connection"
	DefaultResultDBDir  = "/resultdb-connection"
	DefaultRegistryAuth = "/registry-auth/config.json"
)

// Config is the validated framework configuration.
type Config struct {
	NATSURL              string
	Stream               string
	RequestSubject       string
	ReadySubjectTemplate string
	Consumer             string
	Namespace            string
	Project              string
	ExperimentUID        string
	Repository           string
	BaseImage            string
	DetailDB             dbconfig.DatabaseConfig
	ResultDB             dbconfig.DatabaseConfig
	Mounts               Mounts
}

// uidRe matches a Kubernetes-style UID: 8-4-4-4-12 lowercase hex groups.
var uidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// digestRe matches a pinned image digest suffix @sha256:<64 hex>.
var digestRe = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// Load reads and validates the framework configuration from environment
// variables and the mounted Secrets. It returns a descriptive error for any
// missing, malformed, or inconsistent value. It must be called before any NATS
// connection or request handling.
func Load(mounts Mounts) (*Config, error) {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	cfg := &Config{
		NATSURL:              os.Getenv("NATS_URL"),
		Stream:               os.Getenv("TRANSLATOR_STREAM"),
		RequestSubject:       os.Getenv("TRANSLATOR_REQUEST_SUBJECT"),
		ReadySubjectTemplate: os.Getenv("TRANSLATOR_READY_SUBJECT_TEMPLATE"),
		Consumer:             os.Getenv("TRANSLATOR_CONSUMER"),
		Namespace:            os.Getenv("SIMULATIONPROJECTNAMESPACE"),
		Project:              os.Getenv("SIMULATIONPROJECTNAME"),
		ExperimentUID:        os.Getenv("SIMULATIONEXPERIMENTUID"),
		Repository:           os.Getenv("REPOSITORY"),
		BaseImage:            os.Getenv("BASEIMAGE"),
		Mounts:               mounts,
	}

	if cfg.NATSURL == "" {
		add("NATS_URL is required")
	} else if err := validateNATSURL(cfg.NATSURL); err != nil {
		add("%v", err)
	}
	if cfg.Stream == "" {
		add("TRANSLATOR_STREAM is required")
	} else if err := validateJetStreamName(cfg.Stream); err != nil {
		add("TRANSLATOR_STREAM: %v", err)
	}
	if cfg.RequestSubject == "" {
		add("TRANSLATOR_REQUEST_SUBJECT is required")
	} else {
		ns, proj, err := subject.ParseRequestSubject(cfg.RequestSubject)
		if err != nil {
			add("TRANSLATOR_REQUEST_SUBJECT: %v", err)
		} else {
			cfg.Namespace = ns
			cfg.Project = proj
		}
	}
	if cfg.ReadySubjectTemplate == "" {
		add("TRANSLATOR_READY_SUBJECT_TEMPLATE is required")
	} else if err := subject.ValidateReadyTemplate(cfg.ReadySubjectTemplate); err != nil {
		add("%v", err)
	}
	if cfg.Consumer == "" {
		add("TRANSLATOR_CONSUMER is required")
	} else if err := validateJetStreamName(cfg.Consumer); err != nil {
		add("TRANSLATOR_CONSUMER: %v", err)
	}
	// TRANSLATOR_CONSUMER is the UID-specific durable consumer name and must
	// equal translator-<12-char-UID-prefix>. The framework derives the same
	// name from SIMULATIONEXPERIMENTUID for the per-experiment consumer, so a
	// mismatch is an Operator injection error.
	if cfg.Consumer != "" && cfg.ExperimentUID != "" {
		if want := "translator-" + imageref.UIDPrefix(cfg.ExperimentUID); cfg.Consumer != want {
			add("TRANSLATOR_CONSUMER %q must equal the UID-specific durable name %q", cfg.Consumer, want)
		}
	}
	// SIMULATIONPROJECTNAMESPACE and SIMULATIONPROJECTNAME must be valid DNS labels
	// and must match the namespace/project tokens parsed from the request subject.
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
	// If both the request subject and the env identifiers parsed, they must agree.
	if cfg.RequestSubject != "" {
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
	}
	if cfg.ExperimentUID == "" {
		add("SIMULATIONEXPERIMENTUID is required")
	} else if !uidRe.MatchString(cfg.ExperimentUID) {
		add("SIMULATIONEXPERIMENTUID %q is not a valid UID (8-4-4-4-12 lowercase hex)", cfg.ExperimentUID)
	}
	if cfg.Repository == "" {
		add("REPOSITORY is required")
	} else if err := validateRepository(cfg.Repository); err != nil {
		add("REPOSITORY: %v", err)
	}
	if cfg.BaseImage == "" {
		add("BASEIMAGE is required")
	} else if !digestRe.MatchString(cfg.BaseImage) {
		add("BASEIMAGE %q must be a digest-pinned reference ending in @sha256:<64 hex>", cfg.BaseImage)
	}

	// Mounted database connection Secrets.
	detail, err := loadDatabaseConfig(mounts.DetailDBDir)
	if err != nil {
		add("detail database: %v", err)
	} else {
		cfg.DetailDB = detail
	}
	result, err := loadDatabaseConfig(mounts.ResultDBDir)
	if err != nil {
		add("result database: %v", err)
	} else {
		cfg.ResultDB = result
	}

	// Mounted registry Docker config: parse and require basic credentials for
	// the configured base-image and target-repository authorities.
	if mounts.RegistryAuth == "" {
		add("registry auth path is required")
	} else if rc, err := registryauth.LoadFile(mounts.RegistryAuth); err != nil {
		add("registry auth: %v", err)
	} else {
		if cfg.BaseImage != "" {
			if authority, aerr := registryauth.Authority(cfg.BaseImage); aerr != nil {
				add("base image authority: %v", aerr)
			} else if _, _, rerr := rc.ResolveBasicAuth(authority); rerr != nil {
				add("base image registry %q: %v", authority, rerr)
			}
		}
		if cfg.Repository != "" {
			if authority, aerr := registryauth.Authority(cfg.Repository); aerr != nil {
				add("repository authority: %v", aerr)
			} else if _, _, rerr := rc.ResolveBasicAuth(authority); rerr != nil {
				add("target repository registry %q: %v", authority, rerr)
			}
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("translator configuration is invalid:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return cfg, nil
}

// validateNATSURL rejects an empty URL and any URL carrying user information.
// The framework accepts no NATS credentials through the URL.
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

// validateJetStreamName rejects empty names and names containing whitespace or
// the JetStream stream/subject wildcard characters. It mirrors the constraints
// the Operator and Scenario Manager apply to stream and durable-consumer
// names.
func validateJetStreamName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if strings.ContainsAny(name, " \t\r\n*>\x00") {
		return fmt.Errorf("name %q must not contain whitespace or stream wildcard characters", name)
	}
	return nil
}

// validateRepository rejects empty repositories and any repository carrying a
// tag, digest, or whitespace. A `:` is permitted only in the first path
// component (the registry host:port); a `:` anywhere else would be a tag, and a
// `@` anywhere would be a digest.
func validateRepository(repo string) error {
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf("repository must not be empty")
	}
	if strings.ContainsAny(repo, " \t\r\n\x00") {
		return fmt.Errorf("repository %q must not contain whitespace", repo)
	}
	if strings.Contains(repo, "@") {
		return fmt.Errorf("repository %q must not contain a digest", repo)
	}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) == 2 && strings.Contains(parts[1], ":") {
		return fmt.Errorf("repository %q must not contain a tag (a ':' is allowed only in the registry host:port component)", repo)
	}
	return nil
}

// loadDatabaseConfig reads the five connection files from dir and validates the
// resulting DatabaseConfig.
func loadDatabaseConfig(dir string) (dbconfig.DatabaseConfig, error) {
	var c dbconfig.DatabaseConfig
	if dir == "" {
		return c, fmt.Errorf("connection directory is empty")
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
		return c, fmt.Errorf("port %q is not an integer", portStr)
	}
	c = dbconfig.DatabaseConfig{Host: host, Port: port, DBName: dbname, User: user, Password: password}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// readKey reads and trims a single connection file. An empty value is an error.
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

// UIDPrefix12 returns the first 12 lowercase hexadecimal characters of the
// experiment UID after removing hyphens, used to construct the deterministic
// pushed runner tag. It delegates to imageref.UIDPrefix so the tag, consumer
// name, and runner Job name share one canonical prefix derivation.
func (c *Config) UIDPrefix12() string {
	return imageref.UIDPrefix(c.ExperimentUID)
}
