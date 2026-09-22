package generator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/dbconfig"
	"github.com/D4NS3U/cbse/component-templates/translator/internal/detaildb"
)

// fakeRows serves scripted rows of four ints.
type fakeRows struct {
	rows [][]int
	idx  int
}

func (r *fakeRows) Next() bool {
	if r.idx < len(r.rows) {
		r.idx++
		return true
	}
	return false
}
func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.idx-1]
	for i := range dest {
		*(dest[i].(*int)) = row[i]
	}
	return nil
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Err() error   { return nil }

type fakeConn struct {
	q func() (detaildb.Rows, error)
}

func (c *fakeConn) Exec(context.Context, string, ...any) error { return nil }
func (c *fakeConn) Query(context.Context, string, ...any) (detaildb.Rows, error) {
	return c.q()
}
func (c *fakeConn) Close(context.Context) error { return nil }

type fakeConnector struct {
	conn func() (detaildb.Rows, error)
}

func (f fakeConnector) Connect(ctx context.Context, host string, port int32, user, password, dbname string) (detaildb.Conn, error) {
	return &fakeConn{q: f.conn}, nil
}

func TestValidateRecipe(t *testing.T) {
	g := NewExampleGenerator()
	cases := []struct {
		name string
		raw  string
		want int
		err  bool
	}{
		{"valid", `{"parameterset_id": 1}`, 1, false},
		{"valid larger", `{"parameterset_id": 42}`, 42, false},
		{"missing", ``, 0, true},
		{"null", `null`, 0, true},
		{"malformed", `{not json}`, 0, true},
		{"non-object", `5`, 0, true},
		{"array", `[1,2]`, 0, true},
		{"missing key", `{"other": 1}`, 0, true},
		{"null id", `{"parameterset_id": null}`, 0, true},
		{"non-int", `{"parameterset_id": "1"}`, 0, true},
		{"zero", `{"parameterset_id": 0}`, 0, true},
		{"negative", `{"parameterset_id": -1}`, 0, true},
		{"extra field", `{"parameterset_id": 1, "other": 2}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := g.ValidateRecipe(json.RawMessage(tc.raw))
			if tc.err {
				if err == nil {
					t.Fatalf("ValidateRecipe(%q) = %d, want error", tc.raw, id)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRecipe(%q) err = %v", tc.raw, err)
			}
			if id != tc.want {
				t.Fatalf("ValidateRecipe(%q) = %d, want %d", tc.raw, id, tc.want)
			}
		})
	}
}

func TestGenerateWritesBuildContext(t *testing.T) {
	g := NewExampleGenerator()
	g.Connector = fakeConnector{conn: func() (detaildb.Rows, error) {
		return &fakeRows{rows: [][]int{{2, 4, 100, 1001}}}, nil
	}}
	ws := t.TempDir()
	detail := dbconfig.DatabaseConfig{Host: "127.0.0.1", Port: 5432, DBName: "detaildb", User: "du", Password: "dpswd"}
	result := dbconfig.DatabaseConfig{Host: "result.example.com", Port: 5433, DBName: "resultdb", User: "ru", Password: "rpswd"}
	err := g.Generate(context.Background(), GenerationInput{
		ScenarioID:         7,
		TranslationAttempt: 1,
		RecipeInfo:         json.RawMessage(`{"parameterset_id": 1}`),
		BaseImage:          "registry.example.com/proj/runner-base@sha256:" + strings.Repeat("a", 64),
		Workspace:          ws,
		DetailDatabase:     detail,
		ResultDatabase:     result,
	})
	if err != nil {
		t.Fatalf("Generate err = %v", err)
	}
	df, err := os.ReadFile(filepath.Join(ws, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "FROM registry.example.com/proj/runner-base@sha256:") {
		t.Fatalf("Dockerfile missing digest-pinned FROM: %s", df)
	}
	if !strings.Contains(string(df), "USER 1000:1000") {
		t.Fatalf("Dockerfile missing non-root user: %s", df)
	}
	if !strings.Contains(string(df), `ENTRYPOINT ["python3", "/runner/main.py"]`) {
		t.Fatalf("Dockerfile missing entrypoint: %s", df)
	}
	sj, err := os.ReadFile(filepath.Join(ws, "runner", "scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sm map[string]int
	if err := json.Unmarshal(sj, &sm); err != nil {
		t.Fatal(err)
	}
	if sm["scenario_id"] != 7 || sm["parameterset_id"] != 1 || sm["arrival_rate"] != 2 || sm["seed_policy"] != 1001 {
		t.Fatalf("scenario.json = %v", sm)
	}
	rj, err := os.ReadFile(filepath.Join(ws, "runner", "resultdb.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rj), "rpswd") {
		t.Fatalf("resultdb.json missing ResultDB password")
	}
	if strings.Contains(string(rj), "dpswd") {
		t.Fatalf("resultdb.json must not contain DetailDB password")
	}
	for _, p := range []string{"main.py", "model.py", "resultdb.py", "database_endpoint/dns.py", "database_endpoint/ipv4.py", "database_endpoint/ipv6.py", "database_endpoint/dispatcher.py"} {
		if _, err := os.Stat(filepath.Join(ws, "runner", p)); err != nil {
			t.Fatalf("missing embedded runner file %s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, "runner", "database_endpoint", "test_dispatcher.py")); err == nil {
		t.Fatal("test_dispatcher.py must not be embedded in the runner")
	}
}

func TestGenerateBadRecipeFailsBeforeLookup(t *testing.T) {
	calls := 0
	g := NewExampleGenerator()
	g.Connector = fakeConnector{conn: func() (detaildb.Rows, error) {
		calls++
		return &fakeRows{rows: [][]int{{2, 4, 100, 1001}}}, nil
	}}
	err := g.Generate(context.Background(), GenerationInput{
		ScenarioID:         1,
		TranslationAttempt: 1,
		RecipeInfo:         json.RawMessage(`{"parameterset_id": 0}`),
		BaseImage:          "img@sha256:" + strings.Repeat("a", 64),
		Workspace:          t.TempDir(),
		DetailDatabase:     dbconfig.DatabaseConfig{Host: "127.0.0.1", Port: 5432, DBName: "d", User: "u", Password: "p"},
		ResultDatabase:     dbconfig.DatabaseConfig{Host: "127.0.0.1", Port: 5432, DBName: "d", User: "u", Password: "p"},
	})
	if err == nil {
		t.Fatal("bad recipe must fail")
	}
	if calls != 0 {
		t.Fatalf("lookup must not run on a bad recipe; connector calls = %d", calls)
	}
}
