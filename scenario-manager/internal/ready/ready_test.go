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

package ready

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/communication"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testRepo   = "registry.example.com/proj/runner"
	testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testImage  = "registry.example.com/proj/runner@sha256:" + testDigest
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(s); err != nil {
		t.Fatalf("add alpha4 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add core/v1 to scheme: %v", err)
	}
	return s
}

func inProgressExperiment(namespace, name, repo string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID("uid-1")},
		Spec: experimentalpha4.SimulationExperimentSpec{
			Translator: experimentalpha4.TranslatorSpec{
				Image:      "registry.example.com/trans:v1",
				Repository: repo,
			},
		},
		Status: experimentalpha4.SimulationExperimentStatus{Phase: "InProgress"},
	}
}

// fakeDB is a minimal persistence.DB for the ready workflow. The ready handler
// only calls ExecContext (for the guarded transitions); QueryRowContext and
// QueryContext are never invoked by the handler and return nil.
type fakeDB struct {
	rowsAffected int64
	err          error
	called       bool
	lastQuery    string
}

func (f *fakeDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	f.called = true
	f.lastQuery = query
	if f.err != nil {
		return nil, f.err
	}
	return fakeResult{rows: f.rowsAffected}, nil
}
func (f *fakeDB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}
func (f *fakeDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

type fakeResult struct{ rows int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build()
}

func readyMsg(namespace, project string, scenarioID, attempt int, image string) communication.TranslatorReadyMessage {
	return communication.TranslatorReadyMessage{
		ProjectNamespace:   namespace,
		ProjectName:        project,
		ScenarioID:         scenarioID,
		TranslationAttempt: attempt,
		ContainerImage:     image,
	}
}

func TestHandleImageMatchAdvancesStartingRunners(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, testImage))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !db.called {
		t.Fatal("expected MarkScenarioStartingRunners DB call")
	}
	if !strings.Contains(db.lastQuery, "StartingRunners") && !strings.Contains(db.lastQuery, "starting") {
		// The query sets state to the StartingRunners constant via a placeholder;
		// just confirm a guarded UPDATE was issued.
		if !strings.Contains(db.lastQuery, "UPDATE") {
			t.Fatalf("lastQuery = %q; want an UPDATE", db.lastQuery)
		}
	}
}

func TestHandleImageStaleAttemptIsPoison(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 0} // guarded transition matched no row
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, testImage))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled (stale poison)", res.Status)
	}
	if res.Reason == "" || !strings.Contains(res.Reason, "stale") {
		t.Fatalf("reason = %q; want stale", res.Reason)
	}
}

func TestHandleImageRepositoryMismatchIsPoison(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", "registry.example.com/other/runner")
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 1} // should NOT be called
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, testImage))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled (poison)", res.Status)
	}
	if db.called {
		t.Fatal("repository mismatch must not persist or create a Job")
	}
}

func TestHandleImageInvalidDigestIsPoison(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, "registry.example.com/proj/runner:v1"))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled (invalid digest poison)", res.Status)
	}
	if db.called {
		t.Fatal("invalid digest must not persist")
	}
}

func TestHandleEmptyImageConsumesAttempt(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, ""))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !strings.Contains(res.Reason, "consumed") {
		t.Fatalf("reason = %q; want consumed attempt", res.Reason)
	}
}

func TestHandleEmptyImageAtLimitMovesFailed(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	// attempt == maxAttempts -> finalState Failed; the guarded UPDATE still
	// affects one row, so the reason records the Failed outcome.
	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 3, ""))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled", res.Status)
	}
	if !strings.Contains(res.Reason, "Failed") {
		t.Fatalf("reason = %q; want Failed at limit", res.Reason)
	}
}

func TestHandleEmptyImageStaleIsHandled(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{rowsAffected: 0}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, ""))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled (stale)", res.Status)
	}
}

func TestHandleDBErrorIsRetry(t *testing.T) {
	exp := inProgressExperiment("ns", "proj", testRepo)
	k8s := fakeK8s(t, exp)
	db := &fakeDB{err: errors.New("connection refused")}
	h := NewHandler(k8s, db, 3)

	// Non-empty image path.
	if res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, testImage)); res.Status != communication.TranslatorReadyRetry {
		t.Fatalf("non-empty db error: status = %q; want Retry", res.Status)
	}
	// Empty image path.
	db2 := &fakeDB{err: errors.New("connection refused")}
	h2 := NewHandler(k8s, db2, 3)
	if res := h2.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, "")); res.Status != communication.TranslatorReadyRetry {
		t.Fatalf("empty db error: status = %q; want Retry", res.Status)
	}
}

func TestHandleExperimentGoneIsPoison(t *testing.T) {
	// No experiment in the fake client.
	k8s := fakeK8s(t)
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "missing", 7, 1, testImage))
	if res.Status != communication.TranslatorReadyHandled {
		t.Fatalf("status = %q; want Handled (experiment gone poison)", res.Status)
	}
	if db.called {
		t.Fatal("a gone experiment must not persist")
	}
}

func TestHandleExperimentTransientErrorIsRetry(t *testing.T) {
	k8s := &errK8s{Client: fakeK8s(t), err: errors.New("apiserver unavailable")}
	db := &fakeDB{rowsAffected: 1}
	h := NewHandler(k8s, db, 3)

	res := h.Handle(context.Background(), readyMsg("ns", "proj", 7, 1, testImage))
	if res.Status != communication.TranslatorReadyRetry {
		t.Fatalf("status = %q; want Retry", res.Status)
	}
}

// errK8s wraps a real client.Client and overrides Get to always fail with a
// transient (non-NotFound) error, exercising the retry branch.
type errK8s struct {
	client.Client
	err error
}

func (e *errK8s) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return e.err
}
