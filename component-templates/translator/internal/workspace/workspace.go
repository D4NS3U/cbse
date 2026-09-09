// Package workspace owns the per-attempt on-disk workspace and the single
// credential-free durable outcome marker (ready-outcome.json) for the
// reference Translator.
//
// The attempt workspace is /workspace/scenario-<scenario-id>/attempt-<attempt>.
// The framework checks for a retained outcome before removing or recreating
// build input: only an attempt with no retained outcome and no recoverable
// registry tag has its build directory recreated. It retains the workspace
// until ready publication and server-confirmed request acknowledgement both
// succeed, then removes the attempt workspace.
//
// ready-outcome.json contains outcome (success|empty_failure), scenario ID,
// translation attempt, ready subject, and full experiment UID. A success
// additionally contains the deterministic tag and digest; an empty failure
// contains an empty image and a short non-sensitive failure class. The complete
// file is written to a temporary file in the same directory and renamed over
// the marker before publishing ready, so a crash never leaves a partial marker.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	markerName     = "ready-outcome.json"
	markerTmpName  = ".ready-outcome.json.tmp"
	buildDirName   = "runner"
	dockerfileName = "Dockerfile"
)

// Outcome values.
const (
	OutcomeSuccess      = "success"
	OutcomeEmptyFailure = "empty_failure"
)

// Marker is the single credential-free durable outcome format.
type Marker struct {
	Outcome       string `json:"outcome"`
	ScenarioID    int    `json:"scenario_id"`
	Attempt       int    `json:"translation_attempt"`
	ReadySubject  string `json:"ready_subject"`
	ExperimentUID string `json:"experiment_uid"`
	// Success-only fields.
	Tag    string `json:"tag,omitempty"`
	Digest string `json:"digest,omitempty"`
	// Empty-failure-only fields.
	EmptyImage   bool   `json:"empty_image,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
}

// Workspace manages the per-attempt directories under a base workspace root.
type Workspace struct {
	Root string
}

// New returns a Workspace rooted at root (typically /workspace).
func New(root string) *Workspace {
	return &Workspace{Root: root}
}

// AttemptDir returns the absolute attempt directory path for the given
// scenario ID and translation attempt.
func (w *Workspace) AttemptDir(scenarioID, attempt int) string {
	return filepath.Join(w.Root, fmt.Sprintf("scenario-%d", scenarioID), fmt.Sprintf("attempt-%d", attempt))
}

// EnsureAttemptDir creates the attempt directory with 0755 permissions and
// returns its path.
func (w *Workspace) EnsureAttemptDir(scenarioID, attempt int) (string, error) {
	dir := w.AttemptDir(scenarioID, attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create attempt dir: %w", err)
	}
	return dir, nil
}

// RemoveAttemptDir removes the attempt workspace after a terminal
// acknowledged outcome. A missing directory is success.
func (w *Workspace) RemoveAttemptDir(scenarioID, attempt int) error {
	dir := w.AttemptDir(scenarioID, attempt)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove attempt dir: %w", err)
	}
	// Best-effort: remove the now-empty scenario directory.
	_ = os.Remove(filepath.Dir(dir))
	return nil
}

// MarkerPath returns the marker file path within dir.
func MarkerPath(dir string) string {
	return filepath.Join(dir, markerName)
}

// WriteMarker atomically writes the outcome marker to dir: it writes the
// complete file to a temporary file in the same directory and renames it over
// the marker. A crash therefore never leaves a partial marker.
func WriteMarker(dir string, m Marker) error {
	if err := validateMarkerFields(m); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}
	tmp := filepath.Join(dir, markerTmpName)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write marker temp: %w", err)
	}
	if err := os.Rename(tmp, MarkerPath(dir)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename marker: %w", err)
	}
	return nil
}

// ReadMarker reads the marker from dir. If no marker exists it returns
// (nil, false, nil).
func ReadMarker(dir string) (*Marker, bool, error) {
	data, err := os.ReadFile(MarkerPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read marker: %w", err)
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false, fmt.Errorf("unmarshal marker: %w", err)
	}
	return &m, true, nil
}

// MarkerExists reports whether a marker file exists in dir.
func MarkerExists(dir string) bool {
	_, err := os.Stat(MarkerPath(dir))
	return err == nil
}

// ValidateMarker reports whether m is a well-formed marker that belongs to the
// given experiment UID, scenario ID, attempt, and ready subject, with
// outcome-specific fields consistent with its outcome. A valid marker is
// authoritative for that attempt.
func ValidateMarker(m *Marker, uid string, scenarioID, attempt int, readySubject string) error {
	if m == nil {
		return errors.New("nil marker")
	}
	if err := validateMarkerFields(*m); err != nil {
		return err
	}
	if m.ExperimentUID != uid {
		return fmt.Errorf("marker experiment UID %q != %q", m.ExperimentUID, uid)
	}
	if m.ScenarioID != scenarioID {
		return fmt.Errorf("marker scenario ID %d != %d", m.ScenarioID, scenarioID)
	}
	if m.Attempt != attempt {
		return fmt.Errorf("marker attempt %d != %d", m.Attempt, attempt)
	}
	if m.ReadySubject != readySubject {
		return fmt.Errorf("marker ready subject %q != %q", m.ReadySubject, readySubject)
	}
	return nil
}

// validateMarkerFields validates the outcome value and outcome-specific fields
// without comparing against a request identity.
func validateMarkerFields(m Marker) error {
	switch m.Outcome {
	case OutcomeSuccess:
		if m.Tag == "" {
			return errors.New("success marker missing tag")
		}
		if m.Digest == "" {
			return errors.New("success marker missing digest")
		}
		if m.EmptyImage {
			return errors.New("success marker must not set empty_image")
		}
		if m.FailureClass != "" {
			return errors.New("success marker must not set failure_class")
		}
	case OutcomeEmptyFailure:
		if !m.EmptyImage {
			return errors.New("empty_failure marker must set empty_image")
		}
		if m.FailureClass == "" {
			return errors.New("empty_failure marker missing failure_class")
		}
		if m.Tag != "" || m.Digest != "" {
			return errors.New("empty_failure marker must not set tag or digest")
		}
	default:
		return fmt.Errorf("unknown outcome %q", m.Outcome)
	}
	if m.ScenarioID <= 0 {
		return errors.New("marker scenario ID must be positive")
	}
	if m.Attempt <= 0 {
		return errors.New("marker attempt must be positive")
	}
	if m.ReadySubject == "" {
		return errors.New("marker ready subject is empty")
	}
	if m.ExperimentUID == "" {
		return errors.New("marker experiment UID is empty")
	}
	return nil
}

// BuildInputExists reports whether a generated build context (runner/ dir or
// Dockerfile) exists in dir.
func BuildInputExists(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, buildDirName)); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, dockerfileName)); err == nil {
		return true
	}
	return false
}

// RemoveBuildInput removes the generated build context (runner/ dir and
// Dockerfile) from dir so a fresh generation can write a complete context. A
// missing build context is success.
func RemoveBuildInput(dir string) error {
	for _, name := range []string{buildDirName, dockerfileName} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("remove build input %s: %w", name, err)
		}
	}
	return nil
}
