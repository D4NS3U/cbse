package informer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	"github.com/D4NS3U/cbse/scenario-manager/internal/lifecycle"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(s); err != nil {
		t.Fatalf("add alpha4: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("add batch/v1: %v", err)
	}
	return s
}

func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&experimentalpha4.SimulationExperiment{}).
		Build()
}

// experiment builds a SimulationExperiment fixture with the given phase, an
// optional SM finalizer, and an optional deletionTimestamp (deleting=true).
func experiment(namespace, name, uid, phase string, finalizer bool, deleting bool) *experimentalpha4.SimulationExperiment {
	exp := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: types.UID(uid)},
		Status:     experimentalpha4.SimulationExperimentStatus{Phase: phase},
	}
	if finalizer {
		exp.Finalizers = []string{lifecycle.FinalizerName}
	}
	if deleting {
		exp.DeletionTimestamp = &metav1.Time{Time: time.Unix(1700000000, 0)}
	}
	return exp
}

// recordingStore is a lifecycle.ProjectStore fake. failProjectID controls how
// many times ProjectIDByNamespaceAndName returns a transient error before
// succeeding, so tests can exercise the fixed retry cadence.
type recordingStore struct {
	mu             sync.Mutex
	projectID      int
	failProjectID  int32 // remaining transient failures
	projectIDCalls []string
	failCalls      []int
	deleteCalls    []string
	failErr        error
	deleteErr      error
}

func (s *recordingStore) ProjectIDByNamespaceAndName(_ context.Context, namespace, project string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectIDCalls = append(s.projectIDCalls, namespace+"/"+project)
	if atomic.LoadInt32(&s.failProjectID) > 0 {
		atomic.AddInt32(&s.failProjectID, -1)
		return 0, errors.New("transient project lookup")
	}
	return s.projectID, nil
}
func (s *recordingStore) MarkScenariosFailedForProject(_ context.Context, projectID int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCalls = append(s.failCalls, projectID)
	if s.failErr != nil {
		return 0, s.failErr
	}
	return 0, nil
}
func (s *recordingStore) DeleteProjectByNamespaceAndName(_ context.Context, namespace, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls = append(s.deleteCalls, namespace+"/"+project)
	return s.deleteErr
}

// recordingMsg is a lifecycle.MessagingCleaner fake. failConsumer controls how
// many times DeleteTranslatorConsumer fails before succeeding.
type recordingMsg struct {
	mu                sync.Mutex
	consumerDeletions []string
	purges            []string
	failConsumer      int32
}

func (m *recordingMsg) DeleteTranslatorConsumer(_ context.Context, uid, namespace, project string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.consumerDeletions = append(m.consumerDeletions, uid+"/"+namespace+"/"+project)
	if atomic.LoadInt32(&m.failConsumer) > 0 {
		atomic.AddInt32(&m.failConsumer, -1)
		return errors.New("consumer delete transient")
	}
	return nil
}
func (m *recordingMsg) PurgeSubject(_ context.Context, stream, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purges = append(m.purges, stream+":"+subject)
	return nil
}

// newDispatcher builds a Dispatcher with short retry cadence and a root context
// for tests.
func newDispatcher(t *testing.T, k8s client.Client, store lifecycle.ProjectStore, msg lifecycle.MessagingCleaner, register func(context.Context, string, string) error) *Dispatcher {
	t.Helper()
	rootCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var registerCalls []string
	var registerMu sync.Mutex
	if register == nil {
		register = func(ctx context.Context, namespace, project string) error {
			registerMu.Lock()
			registerCalls = append(registerCalls, namespace+"/"+project)
			registerMu.Unlock()
			return nil
		}
	}
	d := NewDispatcher(k8s, store, msg, register)
	d.SetRetryCadence(2 * time.Millisecond)
	d.SetRootContext(rootCtx)
	return d
}

// waitUntil polls fn until it returns true or the timeout elapses, returning
// fn's last value. It is a coarse sync aid for goroutine-backed actions.
func waitUntil(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return fn()
}

func TestHandleAddNonDeletingRegistersProject(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseInProgress, false, false)
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 1}
	msg := &recordingMsg{}
	var registered string
	var mu sync.Mutex
	d := NewDispatcher(k8s, store, msg, func(ctx context.Context, namespace, project string) error {
		mu.Lock()
		registered = namespace + "/" + project
		mu.Unlock()
		return nil
	})
	d.SetRetryCadence(2 * time.Millisecond)
	rootCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.SetRootContext(rootCtx)

	d.HandleAdd(exp)

	mu.Lock()
	got := registered
	mu.Unlock()
	if got != "ns/proj" {
		t.Fatalf("registered = %q; want ns/proj", got)
	}
	// The finalizer was added.
	current := &experimentalpha4.SimulationExperiment{}
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "proj"}, current); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(current.Finalizers) != 1 || current.Finalizers[0] != lifecycle.FinalizerName {
		t.Fatalf("finalizers = %v; want [%s]", current.Finalizers, lifecycle.FinalizerName)
	}
}

func TestHandleAddDeletingRunsCleanup(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseInProgress, true, true) // deleting with finalizer
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 1}
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleAdd(exp)
	// Deletion cleanup runs in a goroutine and succeeds: the consumer is
	// deleted, the project row is deleted, and the finalizer is removed. Once
	// the finalizer is removed the deleting object is gone, so NotFound is the
	// completion signal.
	if !waitUntil(2*time.Second, func() bool {
		current := &experimentalpha4.SimulationExperiment{}
		err := k8s.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "proj"}, current)
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil {
			return false
		}
		return len(current.Finalizers) == 0
	}) {
		t.Fatalf("deletion cleanup did not complete")
	}
	msg.mu.Lock()
	defer msg.mu.Unlock()
	if len(msg.consumerDeletions) != 1 {
		t.Fatalf("consumer deletions = %v; want 1", msg.consumerDeletions)
	}
}

func TestHandleUpdateErrorDispatchesTerminal(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseError, true, false)
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 5}
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleUpdate(nil, exp)
	if !waitUntil(time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.failCalls) == 1 && store.failCalls[0] == 5
	}) {
		t.Fatalf("terminal action not applied: %+v", store)
	}
	// Terminal action does NOT delete the project row or the consumer.
	msg.mu.Lock()
	consumers := len(msg.consumerDeletions)
	msg.mu.Unlock()
	if consumers != 0 {
		t.Fatal("terminal action must not delete the translator consumer")
	}
}

func TestHandleUpdateCompletedIsNoOp(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseCompleted, true, false)
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 5}
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleUpdate(nil, exp)
	// Completed closes the gate only: no terminal bulk update, no consumer
	// deletion, no purge, no finalizer removal.
	if !waitUntil(200*time.Millisecond, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.failCalls) == 0
	}) {
		t.Fatalf("completed must not bulk-update scenarios: %+v", store)
	}
	msg.mu.Lock()
	defer msg.mu.Unlock()
	if len(msg.consumerDeletions) != 0 || len(msg.purges) != 0 {
		t.Fatalf("completed must not clean messaging artifacts: %+v", msg)
	}
}

func TestHandleUpdateDeletingHasPrecedenceOverPhase(t *testing.T) {
	// deletionTimestamp AND phase=Error: only deletion cleanup runs.
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseError, true, true)
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 5}
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleUpdate(nil, exp)
	if !waitUntil(time.Second, func() bool {
		msg.mu.Lock()
		defer msg.mu.Unlock()
		return len(msg.consumerDeletions) == 1
	}) {
		t.Fatalf("deletion cleanup not run: %+v", msg)
	}
	// Deletion cleanup must not run the terminal bulk update.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failCalls) != 0 {
		t.Fatalf("deletion must take precedence over terminal: failCalls=%v", store.failCalls)
	}
}

func TestHandleUpdateStaleUIDIgnored(t *testing.T) {
	// The live object has a different UID than the event (replacement).
	live := experiment("ns", "proj", "uid-live", lifecycle.PhaseError, true, false)
	k8s := fakeK8s(t, live)
	store := &recordingStore{projectID: 5}
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	// Event carries the OLD uid; re-get returns the new-uid replacement.
	stale := experiment("ns", "proj", "uid-old", lifecycle.PhaseError, true, false)
	d.HandleUpdate(nil, stale)

	// Give any (incorrect) action time to run; nothing should happen.
	time.Sleep(50 * time.Millisecond)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failCalls) != 0 {
		t.Fatalf("stale UID must not dispatch terminal: %+v", store)
	}
	msg.mu.Lock()
	defer msg.mu.Unlock()
	if len(msg.consumerDeletions) != 0 {
		t.Fatalf("stale UID must not dispatch cleanup: %+v", msg)
	}
}

func TestFailedTerminalRetriesThenSucceeds(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseError, true, false)
	k8s := fakeK8s(t, exp)
	// ProjectIDByNamespaceAndName fails twice (transient), then succeeds.
	store := &recordingStore{projectID: 7}
	atomic.StoreInt32(&store.failProjectID, 2)
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleUpdate(nil, exp)
	// The action retries on the short cadence and eventually succeeds.
	if !waitUntil(2*time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.failCalls) == 1 && store.failCalls[0] == 7
	}) {
		t.Fatalf("retry did not succeed: %+v", store)
	}
	// At least two transient failures were observed before success.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.projectIDCalls) < 3 {
		t.Fatalf("expected >=3 project lookups (2 transient + 1 success); got %d", len(store.projectIDCalls))
	}
}

func TestClosingOneGateDoesNotDisruptAnother(t *testing.T) {
	expA := experiment("ns", "a", "uid-a", lifecycle.PhaseError, true, false)
	expB := experiment("ns", "b", "uid-b", lifecycle.PhaseError, true, false)
	k8s := fakeK8s(t, expA, expB)
	store := &recordingStore{projectID: 9}
	// expA's terminal action fails forever (project lookup always transient).
	atomic.StoreInt32(&store.failProjectID, 1<<30)
	msg := &recordingMsg{}
	d := newDispatcher(t, k8s, store, msg, nil)

	d.HandleUpdate(nil, expA) // A starts retrying forever
	// Allow A to start retrying.
	time.Sleep(20 * time.Millisecond)

	// Reset the store so B succeeds immediately.
	atomic.StoreInt32(&store.failProjectID, 0)
	d.HandleUpdate(nil, expB) // B should complete
	if !waitUntil(2*time.Second, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		// B's project id is 9; find it in failCalls.
		for _, id := range store.failCalls {
			if id == 9 {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("B did not complete while A retries: %+v", store)
	}

	// Closing A's gate (a new event for a) cancels A's retry without affecting B.
	d.HandleUpdate(nil, expA)
	// Snapshot should eventually be empty (A's gate closed, B's gate completed).
	if !waitUntil(time.Second, func() bool { return len(d.Snapshot()) == 0 }) {
		t.Fatalf("gates not drained: %v", d.Snapshot())
	}
}

func TestShutdownJoinsInFlight(t *testing.T) {
	exp := experiment("ns", "proj", "uid-1", lifecycle.PhaseError, true, false)
	k8s := fakeK8s(t, exp)
	store := &recordingStore{projectID: 1}
	atomic.StoreInt32(&store.failProjectID, 1<<30) // retry forever
	msg := &recordingMsg{}
	rootCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d := NewDispatcher(k8s, store, msg, nil)
	d.SetRetryCadence(2 * time.Millisecond)
	d.SetRootContext(rootCtx)

	d.HandleUpdate(nil, exp)
	time.Sleep(20 * time.Millisecond) // let it start retrying
	if len(d.Snapshot()) != 1 {
		t.Fatalf("expected one in-flight gate; got %v", d.Snapshot())
	}
	done := make(chan struct{})
	go func() {
		d.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not join in-flight action")
	}
	if len(d.Snapshot()) != 0 {
		t.Fatalf("after shutdown gates = %v; want empty", d.Snapshot())
	}
}
