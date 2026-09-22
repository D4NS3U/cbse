// Package generator defines the replaceable model-generator boundary for the
// reference Translator framework. The framework owns request consumption,
// BuildKit, registry, ready publication, and acknowledgement; the generator
// owns only model-specific decisions: validating recipe_info, performing the
// predefined Scenario Detail Database parameter lookup, and writing a complete
// Docker build context into the attempt workspace.
//
// The package ships the reference example generator: a small parameterizable
// SimPy single-server queue whose parameters are looked up from the Scenario
// Detail Database by a positive integer parameterset_id carried in recipe_info.
// A user-defined generator may replace this package without reimplementing the
// handoff protocol, provided it preserves the Generator interface and the
// dependency direction (the generator never consumes NATS messages, calls
// BuildKit, pushes images, publishes ready messages, or acknowledges
// deliveries).
package generator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/databaseendpoint"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/detaildb"
)

// DatabaseConfig is the exact set of PostgreSQL connection fields the framework
// passes to the generator. It mirrors dbconfig.DatabaseConfig so the generator
// package does not export a second type.
type DatabaseConfig = dbconfig.DatabaseConfig

// GenerationInput is the data the framework passes to the generator for one
// scenario attempt. The generator performs the Detail DB lookup using
// DetailDatabase and bakes only ResultDatabase into the generated runner image;
// it must not bake DetailDatabase credentials into the image.
type GenerationInput struct {
	ScenarioID         int
	TranslationAttempt int
	RecipeInfo         json.RawMessage
	ConfidenceMetric   *float64
	BaseImage          string
	Workspace          string
	DetailDatabase     DatabaseConfig
	ResultDatabase     DatabaseConfig
}

// Generator is the replaceable model-generator boundary. Generate writes a
// complete Docker build context into in.Workspace. It must not consume NATS
// messages, call BuildKit, push images, publish ready messages, or acknowledge
// deliveries.
type Generator interface {
	Generate(ctx context.Context, in GenerationInput) error
}

// ExampleGenerator is the reference SimPy single-server-queue generator.
type ExampleGenerator struct {
	// Connector opens the Scenario Detail Database connection. It defaults to
	// the real pgx connector; tests inject a fake.
	Connector detaildb.Connector
	// Resolver resolves DNS hosts for the Detail DB dial. It defaults to the
	// process net.Resolver; tests inject a fake.
	Resolver databaseendpoint.Resolver
}

// NewExampleGenerator returns the reference generator with the real pgx
// connector and process default resolver.
func NewExampleGenerator() *ExampleGenerator {
	return &ExampleGenerator{Connector: detaildb.PgxConnector{}}
}

// RecipeError is a permanent generator-input failure: recipe_info is missing,
// null, malformed, a non-object, lacks a positive integer parameterset_id, or
// carries an unknown field. The framework treats it as a generator failure and
// follows the confirmed empty-image workflow.
type RecipeError struct{ msg string }

func (e *RecipeError) Error() string { return "recipe_info: " + e.msg }

// ValidateRecipe validates that recipe_info is exactly one JSON object
// containing one positive integer lookup key parameterset_id and no other
// fields. It returns the validated parameterset_id.
func (g *ExampleGenerator) ValidateRecipe(recipeInfo json.RawMessage) (int, error) {
	if len(recipeInfo) == 0 || string(recipeInfo) == "null" {
		return 0, &RecipeError{msg: "missing or null"}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(recipeInfo, &obj); err != nil {
		return 0, &RecipeError{msg: "malformed JSON"}
	}
	if len(obj) != 1 {
		return 0, &RecipeError{msg: "must contain exactly one field"}
	}
	raw, ok := obj["parameterset_id"]
	if !ok {
		return 0, &RecipeError{msg: "missing parameterset_id"}
	}
	if string(raw) == "null" {
		return 0, &RecipeError{msg: "parameterset_id is null"}
	}
	var id int
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, &RecipeError{msg: "parameterset_id is not an integer"}
	}
	if id <= 0 {
		return 0, &RecipeError{msg: "parameterset_id must be a positive integer"}
	}
	return id, nil
}

// Generate validates the recipe, performs the predefined Scenario Detail
// Database parameter lookup, and writes the complete Docker build context into
// in.Workspace. It bakes the Result DB connection and the resolved parameters
// into the generated runner image and never bakes the Detail DB connection.
func (g *ExampleGenerator) Generate(ctx context.Context, in GenerationInput) error {
	if in.Workspace == "" {
		return fmt.Errorf("generator: workspace is empty")
	}
	if in.BaseImage == "" {
		return fmt.Errorf("generator: base image is empty")
	}
	parametersetID, err := g.ValidateRecipe(in.RecipeInfo)
	if err != nil {
		return err
	}

	connector := g.Connector
	if connector == nil {
		connector = detaildb.PgxConnector{}
	}
	res, err := detaildb.Lookup(ctx, in.DetailDatabase, parametersetID, g.Resolver, connector)
	if err != nil {
		return err
	}

	runnerDir := filepath.Join(in.Workspace, "runner")
	if err := os.MkdirAll(runnerDir, 0o755); err != nil {
		return fmt.Errorf("generator: create runner dir: %w", err)
	}
	if err := writeEmbeddedRunner(runnerDir); err != nil {
		return err
	}
	if err := writeScenarioJSON(runnerDir, in.ScenarioID, parametersetID, res); err != nil {
		return err
	}
	if err := writeResultDBConfig(runnerDir, in.ResultDatabase); err != nil {
		return err
	}
	if err := writeDockerfile(in.Workspace, in.BaseImage); err != nil {
		return err
	}
	return nil
}

// writeScenarioJSON bakes the scenario identity and the looked-up parameters
// into the runner. These are not credentials.
func writeScenarioJSON(runnerDir string, scenarioID, parametersetID int, res detaildb.LookupResult) error {
	scenario := map[string]int{
		"scenario_id":     scenarioID,
		"parameterset_id": parametersetID,
		"arrival_rate":    res.ArrivalRate,
		"service_rate":    res.ServiceRate,
		"run_duration":    res.RunDuration,
		"seed_policy":     res.SeedPolicy,
	}
	data, err := json.MarshalIndent(scenario, "", "  ")
	if err != nil {
		return fmt.Errorf("generator: marshal scenario.json: %w", err)
	}
	return writeFile(filepath.Join(runnerDir, "scenario.json"), data, 0o644)
}

// writeResultDBConfig bakes the Result DB connection configuration into the
// runner image. This is the trusted-prototype assumption that the generated
// image contains Result DB credentials; the framework never prints or annotates
// these values.
func writeResultDBConfig(runnerDir string, db DatabaseConfig) error {
	cfg := map[string]any{
		"host":     db.Host,
		"port":     db.Port,
		"dbname":   db.DBName,
		"user":     db.User,
		"password": db.Password,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("generator: marshal resultdb.json: %w", err)
	}
	return writeFile(filepath.Join(runnerDir, "resultdb.json"), data, 0o600)
}

// writeDockerfile writes the generated Dockerfile. It uses the digest-pinned
// runner smoke base image, copies only the generated runner files and baked
// Result DB configuration, sets a numeric non-root user, and defines the model
// launcher as its entrypoint. It does not reinstall unversioned dependencies.
func writeDockerfile(workspace, baseImage string) error {
	dockerfile := fmt.Sprintf(`# Generated by the CBSE reference Translator framework. Do not edit.
FROM %s
WORKDIR /runner
COPY --chown=1000:1000 runner/ /runner/
USER 1000:1000
ENTRYPOINT ["python3", "/runner/main.py"]
`, baseImage)
	return writeFile(filepath.Join(workspace, "Dockerfile"), []byte(dockerfile), 0o644)
}

// writeFile writes data to path with the given permissions and ensures the
// parent directory exists.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("generator: write %s: %w", filepath.Base(path), err)
	}
	return nil
}
