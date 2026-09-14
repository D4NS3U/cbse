package lifecycle

import (
	"context"
	"fmt"
	"testing"
	"time"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// testScheme registers the alpha4 SimulationExperiment and batch/v1 Job types
// for the fake client.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(s); err != nil {
		t.Fatalf("add alpha4 to scheme: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("add batch/v1 to scheme: %v", err)
	}
	return s
}

// fakeK8s builds a fake controller-runtime client seeded with the given
// objects.
func fakeK8s(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&experimentalpha4.SimulationExperiment{}).
		Build()
}

// newExperiment returns an InProgress experiment with the SM finalizer present.
func newExperiment(namespace, name string, uid string, phase string, withFinalizer bool, deleting bool) *experimentalpha4.SimulationExperiment {
	exp := &experimentalpha4.SimulationExperiment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
		},
		Status: experimentalpha4.SimulationExperimentStatus{Phase: phase},
	}
	if withFinalizer {
		exp.Finalizers = []string{FinalizerName}
	}
	if deleting {
		exp.DeletionTimestamp = &metav1.Time{Time: time.Unix(1700000000, 0)}
	}
	return exp
}

// verifiedJob returns a runner Job that passes the full ownership check for
// exp with the given scenario id and attempt.
func verifiedJob(exp *experimentalpha4.SimulationExperiment, scenarioID, attempt int) *batchv1.Job {
	controller := true
	block := false
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RunnerJobName(exp.UID, scenarioID, attempt),
			Namespace: exp.Namespace,
			Labels: map[string]string{
				LabelProject:            exp.Name,
				LabelExperimentUID:      string(exp.UID),
				LabelScenarioID:         fmt.Sprintf("%d", scenarioID),
				LabelTranslationAttempt: fmt.Sprintf("%d", attempt),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         experimentalpha4.GroupVersion.String(),
					Kind:               "SimulationExperiment",
					Name:               exp.Name,
					UID:                exp.UID,
					Controller:         &controller,
					BlockOwnerDeletion: &block,
				},
			},
		},
	}
}

// fakeStore is a ProjectStore fake that records calls and returns canned
// results. It is used by terminal and deletion-cleanup tests.
type fakeStore struct {
	projectID      int
	projectErr     error
	failRows       int64
	failErr        error
	deleteErr      error
	projectIDCalls []string
	failCalls      []int
	deleteCalls    []string
}

func (s *fakeStore) ProjectIDByNamespaceAndName(_ context.Context, namespace, project string) (int, error) {
	s.projectIDCalls = append(s.projectIDCalls, namespace+"/"+project)
	if s.projectErr != nil {
		return 0, s.projectErr
	}
	return s.projectID, nil
}

func (s *fakeStore) MarkScenariosFailedForProject(_ context.Context, projectID int) (int64, error) {
	s.failCalls = append(s.failCalls, projectID)
	if s.failErr != nil {
		return 0, s.failErr
	}
	return s.failRows, nil
}

func (s *fakeStore) DeleteProjectByNamespaceAndName(_ context.Context, namespace, project string) error {
	s.deleteCalls = append(s.deleteCalls, namespace+"/"+project)
	return s.deleteErr
}

// fakeMsg is a MessagingCleaner fake that records the cleanup operations in
// order.
type fakeMsg struct {
	consumerDeletions []string
	purges            []purgeCall
	consumerErr       error
	purgeErr          error
}

type purgeCall struct {
	stream, subject string
}

func (m *fakeMsg) DeleteTranslatorConsumer(_ context.Context, uid, namespace, project string) error {
	m.consumerDeletions = append(m.consumerDeletions, uid+"/"+namespace+"/"+project)
	if m.consumerErr != nil {
		return m.consumerErr
	}
	return nil
}

func (m *fakeMsg) PurgeSubject(_ context.Context, stream, subject string) error {
	m.purges = append(m.purges, purgeCall{stream: stream, subject: subject})
	if m.purgeErr != nil {
		return m.purgeErr
	}
	return nil
}

func TestAdmitExperiment(t *testing.T) {
	cases := []struct {
		phase    string
		deleting bool
		want     AdmitDecision
	}{
		{PhaseInProgress, false, Admit},
		{PhasePending, false, AdmitUnavailable},
		{PhaseProvisioning, false, AdmitUnavailable},
		{"", false, AdmitUnavailable},
		{PhaseError, false, AdmitTerminal},
		{PhaseFailed, false, AdmitTerminal},
		{PhaseCompleted, false, AdmitTerminal},
		{PhaseInProgress, true, AdmitTerminal},
		{PhasePending, true, AdmitTerminal},
		{"unknown-phase", false, AdmitTerminal},
	}
	for _, c := range cases {
		exp := newExperiment("ns", "proj", "uid-1", c.phase, true, c.deleting)
		if got := AdmitExperiment(exp); got != c.want {
			t.Errorf("phase=%q deleting=%v: got %s; want %s", c.phase, c.deleting, got, c.want)
		}
	}
	if got := AdmitExperiment(nil); got != AdmitTerminal {
		t.Errorf("nil: got %s; want terminal", got)
	}
}

func TestAdmitDecisionHelpers(t *testing.T) {
	if !Admit.IsAdmitted() || Admit.IsTerminal() {
		t.Error("Admit flags")
	}
	if AdmitUnavailable.IsAdmitted() || AdmitUnavailable.IsTerminal() {
		t.Error("Unavailable flags")
	}
	if AdmitTerminal.IsAdmitted() || !AdmitTerminal.IsTerminal() {
		t.Error("Terminal flags")
	}
}

// clientKey returns the NamespacedName for an object.
func clientKey(obj client.Object) types.NamespacedName {
	return types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
}

// isNotFound reports whether err is an API NotFound error.
func isNotFound(err error) bool { return apierrors.IsNotFound(err) }
