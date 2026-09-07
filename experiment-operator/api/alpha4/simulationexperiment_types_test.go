/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package alpha4_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

const testNamespace = "alpha4-api-tests"

var (
	testClient       client.Client
	testDiscovery    discovery.DiscoveryInterface
	testDynamic      dynamic.Interface
	testEnvironment  *envtest.Environment
	resourceSequence atomic.Uint64
)

func TestMain(m *testing.M) {
	testScheme := runtime.NewScheme()
	if err := experimentalpha4.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "add alpha4 to test scheme: %v\n", err)
		os.Exit(1)
	}
	if err := corev1.AddToScheme(testScheme); err != nil {
		fmt.Fprintf(os.Stderr, "add core API to test scheme: %v\n", err)
		os.Exit(1)
	}

	testEnvironment = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "alpha4", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if assets := localEnvtestAssets(); assets != "" {
		testEnvironment.BinaryAssetsDirectory = assets
	}

	cfg, err := testEnvironment.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start alpha4 envtest: %v\n", err)
		os.Exit(1)
	}

	testClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create alpha4 test client: %v\n", err)
		_ = testEnvironment.Stop()
		os.Exit(1)
	}
	testDiscovery, err = discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create alpha4 discovery client: %v\n", err)
		_ = testEnvironment.Stop()
		os.Exit(1)
	}
	testDynamic, err = dynamic.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create alpha4 dynamic client: %v\n", err)
		_ = testEnvironment.Stop()
		os.Exit(1)
	}

	if err := testClient.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "create alpha4 test namespace: %v\n", err)
		_ = testEnvironment.Stop()
		os.Exit(1)
	}

	code := m.Run()
	if err := testEnvironment.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop alpha4 envtest: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func TestAlpha4CRDIsIsolatedAndStructural(t *testing.T) {
	crd := loadAlpha4CRD(t)
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("isolated CRD has %d versions, want exactly alpha4", len(crd.Spec.Versions))
	}
	version := crd.Spec.Versions[0]
	if version.Name != "alpha4" || !version.Served || !version.Storage {
		t.Fatalf("isolated CRD version = %#v, want served and stored alpha4", version)
	}

	root := version.Schema.OpenAPIV3Schema
	spec := requiredProperty(t, root, "spec")
	translator := requiredProperty(t, spec, "translator")
	requireSchemaRequired(t, translator, "image", "repository", "baseimage", "builderImage", "registryAuthSecretRef")

	registryReference := requiredProperty(t, translator, "registryAuthSecretRef")
	if got := sortedPropertyNames(registryReference); !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("registryAuthSecretRef properties = %v, want only name", got)
	}
	if !hasValidation(registryReference, "self.name == 'cbse-registry-auth'", "registryAuthSecretRef.name") {
		t.Fatal("registryAuthSecretRef is missing the exact-name validation")
	}

	builderResources := requiredProperty(t, translator, "builderResources")
	for _, property := range []string{"limits", "requests"} {
		resources := requiredProperty(t, builderResources, property)
		if resources.AdditionalProperties == nil || resources.AdditionalProperties.Schema == nil {
			t.Fatalf("builderResources.%s is not a structural ResourceList", property)
		}
	}

	runner := requiredProperty(t, spec, "runner")
	jobTemplate := requiredProperty(t, runner, "jobTemplate")
	jobMetadata := requiredProperty(t, jobTemplate, "metadata")
	_ = requiredProperty(t, jobMetadata, "labels")
	_ = requiredProperty(t, jobMetadata, "annotations")
	jobSpec := requiredProperty(t, jobTemplate, "spec")
	podTemplate := requiredProperty(t, jobSpec, "template")
	podMetadata := requiredProperty(t, podTemplate, "metadata")
	_ = requiredProperty(t, podMetadata, "labels")
	_ = requiredProperty(t, podMetadata, "annotations")
	podSpec := requiredProperty(t, podTemplate, "spec")
	_ = requiredProperty(t, podSpec, "containers")
	assertNoPreserveUnknownFields(t, jobTemplate, "spec.runner.jobTemplate")
}

func TestAlpha4UsesExactKubernetesTypes(t *testing.T) {
	translatorType := reflect.TypeOf(experimentalpha4.TranslatorSpec{})
	registryField, ok := translatorType.FieldByName("RegistryAuthSecretRef")
	if !ok || registryField.Type != reflect.TypeOf(corev1.LocalObjectReference{}) {
		t.Fatalf("RegistryAuthSecretRef type = %v, want corev1.LocalObjectReference", registryField.Type)
	}
	builderField, ok := translatorType.FieldByName("BuilderResources")
	if !ok || builderField.Type != reflect.TypeOf((*corev1.ResourceRequirements)(nil)) {
		t.Fatalf("BuilderResources type = %v, want *corev1.ResourceRequirements", builderField.Type)
	}
	runnerType := reflect.TypeOf(experimentalpha4.RunnerSpec{})
	jobField, ok := runnerType.FieldByName("JobTemplate")
	if !ok || jobField.Type != reflect.TypeOf((*batchv1.JobTemplateSpec)(nil)) {
		t.Fatalf("JobTemplate type = %v, want *batchv1.JobTemplateSpec", jobField.Type)
	}
}

func TestEnvtestServesAndStoresOnlyAlpha4(t *testing.T) {
	resources, err := testDiscovery.ServerResourcesForGroupVersion(experimentalpha4.GroupVersion.String())
	if err != nil {
		t.Fatalf("discover alpha4: %v", err)
	}
	if resources.GroupVersion != experimentalpha4.GroupVersion.String() {
		t.Fatalf("discovered group version = %q, want %q", resources.GroupVersion, experimentalpha4.GroupVersion.String())
	}
	for _, legacyVersion := range []string{"alpha2", "alpha3"} {
		_, err := testDiscovery.ServerResourcesForGroupVersion(experimentalpha4.GroupVersion.Group + "/" + legacyVersion)
		if err == nil || !apierrors.IsNotFound(err) {
			t.Fatalf("legacy version %s discovery error = %v, want NotFound", legacyVersion, err)
		}
	}

	created := newExperiment(nextName("stored"))
	if err := testClient.Create(context.Background(), created); err != nil {
		t.Fatalf("create alpha4 experiment: %v", err)
	}
	stored, err := testDynamic.Resource(simulationExperimentGVR()).Namespace(testNamespace).Get(
		context.Background(), created.Name, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("get stored alpha4 experiment: %v", err)
	}
	if stored.GetAPIVersion() != experimentalpha4.GroupVersion.String() {
		t.Fatalf("stored apiVersion = %q, want %q", stored.GetAPIVersion(), experimentalpha4.GroupVersion.String())
	}
}

func TestProjectNameValidation(t *testing.T) {
	for _, name := range []string{"a", "a-1", strings.Repeat("a", 63)} {
		resource := newExperiment(name)
		if err := testClient.Create(context.Background(), resource); err != nil {
			t.Fatalf("valid project name %q rejected: %v", name, err)
		}
	}

	for _, name := range []string{"contains.dot", strings.Repeat("a", 64)} {
		resource := newExperiment(name)
		err := testClient.Create(context.Background(), resource)
		if err == nil || !strings.Contains(err.Error(), "metadata.name") {
			t.Fatalf("invalid project name %q error = %v, want metadata.name rejection", name, err)
		}
	}
}

func TestRegistrySecretReferenceAndRequiredBaseImageAdmission(t *testing.T) {
	for _, name := range []string{"", "another-secret"} {
		resource := newExperiment(nextName("registry"))
		resource.Spec.Translator.RegistryAuthSecretRef.Name = name
		err := testClient.Create(context.Background(), resource)
		if err == nil || !strings.Contains(err.Error(), "registryAuthSecretRef.name") {
			t.Fatalf("registry Secret name %q error = %v, want registryAuthSecretRef.name rejection", name, err)
		}
	}

	resource := newExperiment(nextName("baseimage"))
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(resource)
	if err != nil {
		t.Fatalf("convert experiment to unstructured: %v", err)
	}
	unstructured.RemoveNestedField(raw, "spec", "translator", "baseimage")
	_, err = testDynamic.Resource(simulationExperimentGVR()).Namespace(testNamespace).Create(
		context.Background(), &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "baseimage") {
		t.Fatalf("missing baseimage error = %v, want required-field rejection", err)
	}
}

func TestImmutableDatabaseSubfields(t *testing.T) {
	databaseMutations := []struct {
		name   string
		mutate func(*experimentalpha4.DatabaseSpec)
	}{
		{name: "image", mutate: func(database *experimentalpha4.DatabaseSpec) {
			database.Image = "example.invalid/changed@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "host", mutate: func(database *experimentalpha4.DatabaseSpec) { database.Host = "changed.example.invalid" }},
		{name: "dbname", mutate: func(database *experimentalpha4.DatabaseSpec) { database.DBName = "changed" }},
		{name: "user", mutate: func(database *experimentalpha4.DatabaseSpec) { database.User = "changed" }},
		{name: "password", mutate: func(database *experimentalpha4.DatabaseSpec) { database.Password = "changed" }},
		{name: "serviceType", mutate: func(database *experimentalpha4.DatabaseSpec) {
			database.ServiceType = experimentalpha4.ServiceTypeLoadBalancer
		}},
		{name: "nodePort", mutate: func(database *experimentalpha4.DatabaseSpec) { database.NodePort = int32Pointer(30002) }},
		{name: "port", mutate: func(database *experimentalpha4.DatabaseSpec) { database.Port++ }},
		{name: "command", mutate: func(database *experimentalpha4.DatabaseSpec) { database.Command = []string{"changed"} }},
		{name: "args", mutate: func(database *experimentalpha4.DatabaseSpec) { database.Args = []string{"changed"} }},
	}

	for _, databaseName := range []string{"detailDatabase", "resultDatabase"} {
		for _, mutation := range databaseMutations {
			t.Run(databaseName+"/"+mutation.name, func(t *testing.T) {
				expectImmutableRejection(t, "spec."+databaseName, func(resource *experimentalpha4.SimulationExperiment) {
					if databaseName == "detailDatabase" {
						mutation.mutate(&resource.Spec.DetailDatabase)
						return
					}
					mutation.mutate(&resource.Spec.ResultDatabase)
				})
			})
		}
	}
}

func TestImmutableTranslatorFields(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		mutate func(*experimentalpha4.SimulationExperiment)
	}{
		{name: "image", field: "spec.translator.image", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.Image = "example.invalid/translator@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "repository", field: "spec.translator.repository", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.Repository = "example.invalid/changed"
		}},
		{name: "baseimage", field: "spec.translator.baseimage", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.BaseImage = "example.invalid/base@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "builderImage", field: "spec.translator.builderImage", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.BuilderImage = "example.invalid/builder@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "serviceType", field: "spec.translator.serviceType", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.ServiceType = experimentalpha4.ServiceTypeLoadBalancer
		}},
		{name: "port", field: "spec.translator.port", mutate: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Translator.Port++ }},
		{name: "builderResources", field: "spec.translator.builderResources", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.BuilderResources.Limits[corev1.ResourceCPU] = kresource.MustParse("2")
		}},
		{name: "command", field: "spec.translator.command", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.Command = []string{"changed"}
		}},
		{name: "args", field: "spec.translator.args", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.Args = []string{"changed"}
		}},
		{name: "nodePort", field: "spec.translator.nodePort", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Translator.NodePort = int32Pointer(30002)
		}},
		{name: "jobTemplate", field: "spec.runner.jobTemplate", mutate: func(resource *experimentalpha4.SimulationExperiment) {
			resource.Spec.Runner.JobTemplate.Spec.ActiveDeadlineSeconds = int64Pointer(60)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectImmutableRejection(t, test.field, test.mutate)
		})
	}
}

func TestImmutableOptionalFieldPresence(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		remove func(*experimentalpha4.SimulationExperiment)
		add    func(*experimentalpha4.SimulationExperiment)
	}{
		{
			name: "builderResources", field: "spec.translator.builderResources",
			remove: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Translator.BuilderResources = nil },
			add: func(resource *experimentalpha4.SimulationExperiment) {
				resource.Spec.Translator.BuilderResources = builderResources()
			},
		},
		{
			name: "command", field: "spec.translator.command",
			remove: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Translator.Command = nil },
			add: func(resource *experimentalpha4.SimulationExperiment) {
				resource.Spec.Translator.Command = []string{"translator"}
			},
		},
		{
			name: "args", field: "spec.translator.args",
			remove: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Translator.Args = nil },
			add: func(resource *experimentalpha4.SimulationExperiment) {
				resource.Spec.Translator.Args = []string{"--serve"}
			},
		},
		{
			name: "nodePort", field: "spec.translator.nodePort",
			remove: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Translator.NodePort = nil },
			add: func(resource *experimentalpha4.SimulationExperiment) {
				resource.Spec.Translator.NodePort = int32Pointer(30001)
			},
		},
		{
			name: "jobTemplate", field: "spec.runner.jobTemplate",
			remove: func(resource *experimentalpha4.SimulationExperiment) { resource.Spec.Runner.JobTemplate = nil },
			add: func(resource *experimentalpha4.SimulationExperiment) {
				resource.Spec.Runner.JobTemplate = runnerJobTemplate()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+"/remove", func(t *testing.T) {
			expectImmutableRejection(t, test.field, test.remove)
		})
		t.Run(test.name+"/add", func(t *testing.T) {
			resource := newExperiment(nextName("add"))
			test.remove(resource)
			if err := testClient.Create(context.Background(), resource); err != nil {
				t.Fatalf("create experiment without %s: %v", test.field, err)
			}
			test.add(resource)
			err := testClient.Update(context.Background(), resource)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("adding %s error = %v, want immutable-field rejection", test.field, err)
			}
		})
	}
}

func TestUnchangedImmutableFieldsAllowMetadataAndStatusUpdates(t *testing.T) {
	ctx := context.Background()
	resource := newExperiment(nextName("mutable"))
	if err := testClient.Create(ctx, resource); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	resource.Labels = map[string]string{"supported-update": "true"}
	if err := testClient.Update(ctx, resource); err != nil {
		t.Fatalf("metadata update with unchanged immutable fields: %v", err)
	}

	resource.Status.Phase = "Provisioning"
	resource.Status.Message = "status update"
	resource.Status.Metrics = &experimentalpha4.StatusMetrics{ScenarioCount: 1}
	if err := testClient.Status().Update(ctx, resource); err != nil {
		t.Fatalf("status update with unchanged immutable fields: %v", err)
	}

	stored := &experimentalpha4.SimulationExperiment{}
	if err := testClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: resource.Name}, stored); err != nil {
		t.Fatalf("get updated experiment: %v", err)
	}
	if stored.Labels["supported-update"] != "true" || stored.Status.Phase != "Provisioning" || stored.Status.Metrics.ScenarioCount != 1 {
		t.Fatalf("supported updates were not persisted: labels=%v status=%#v", stored.Labels, stored.Status)
	}
}

func TestUnknownJobTemplateFieldsArePruned(t *testing.T) {
	resource := newExperiment(nextName("unknown"))
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(resource)
	if err != nil {
		t.Fatalf("convert experiment to unstructured: %v", err)
	}
	if err := unstructured.SetNestedField(raw, "not-supported", "spec", "runner", "jobTemplate", "spec", "unknownField"); err != nil {
		t.Fatalf("set unknown raw field: %v", err)
	}

	created, err := testDynamic.Resource(simulationExperimentGVR()).Namespace(testNamespace).Create(
		context.Background(), &unstructured.Unstructured{Object: raw}, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("create experiment containing unknown Job field: %v", err)
	}
	if value, found, err := unstructured.NestedFieldNoCopy(created.Object, "spec", "runner", "jobTemplate", "spec", "unknownField"); err != nil || found {
		t.Fatalf("unknown Job field survived structural pruning: found=%v value=%v err=%v", found, value, err)
	}
}

func expectImmutableRejection(t *testing.T, field string, mutate func(*experimentalpha4.SimulationExperiment)) {
	t.Helper()
	resource := newExperiment(nextName("immutable"))
	if err := testClient.Create(context.Background(), resource); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	mutate(resource)
	err := testClient.Update(context.Background(), resource)
	if err == nil || !strings.Contains(err.Error(), field) {
		t.Fatalf("update %s error = %v, want immutable-field rejection", field, err)
	}
}

func newExperiment(name string) *experimentalpha4.SimulationExperiment {
	return &experimentalpha4.SimulationExperiment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: experimentalpha4.GroupVersion.String(),
			Kind:       "SimulationExperiment",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: experimentalpha4.SimulationExperimentSpec{
			DefaultServiceType: experimentalpha4.ServiceTypeClusterIP,
			DetailDatabase:     databaseSpec("detail"),
			ResultDatabase:     databaseSpec("result"),
			Translator: experimentalpha4.TranslatorSpec{
				Image:                 "example.invalid/translator@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Repository:            "example.invalid/runners",
				BaseImage:             "example.invalid/base@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				BuilderImage:          "example.invalid/builder@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				RegistryAuthSecretRef: corev1.LocalObjectReference{Name: "cbse-registry-auth"},
				BuilderResources:      builderResources(),
				ServiceType:           experimentalpha4.ServiceTypeNodePort,
				NodePort:              int32Pointer(30001),
				Port:                  8080,
				Command:               []string{"translator"},
				Args:                  []string{"--serve"},
			},
			PostProcessingService: experimentalpha4.PostProcessingSpec{
				Image:       "example.invalid/post-processing:latest",
				ServiceType: experimentalpha4.ServiceTypeClusterIP,
				Port:        8080,
			},
			ExperimentalDesignService: experimentalpha4.ExperimentalDesignServiceSpec{
				Design:      "example",
				Image:       "example.invalid/eds:latest",
				ServiceType: experimentalpha4.ServiceTypeClusterIP,
				Port:        8080,
			},
			Runner: experimentalpha4.RunnerSpec{JobTemplate: runnerJobTemplate()},
		},
	}
}

func databaseSpec(name string) experimentalpha4.DatabaseSpec {
	return experimentalpha4.DatabaseSpec{
		Image:       "example.invalid/" + name + "@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DBName:      name,
		User:        "test-user",
		Password:    "test-password",
		ServiceType: experimentalpha4.ServiceTypeNodePort,
		NodePort:    int32Pointer(30001),
		Port:        5432,
		Command:     []string{"database"},
		Args:        []string{"--serve"},
	}
}

func builderResources() *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    kresource.MustParse("1"),
			corev1.ResourceMemory: kresource.MustParse("2Gi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    kresource.MustParse("250m"),
			corev1.ResourceMemory: kresource.MustParse("256Mi"),
		},
	}
}

func runnerJobTemplate() *batchv1.JobTemplateSpec {
	return &batchv1.JobTemplateSpec{
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "runner", Image: "example.invalid/runner:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}
}

func nextName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, resourceSequence.Add(1))
}

func int32Pointer(value int32) *int32 {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func simulationExperimentGVR() schema.GroupVersionResource {
	return experimentalpha4.GroupVersion.WithResource("simulationexperiments")
}

func loadAlpha4CRD(t *testing.T) *extensionsv1.CustomResourceDefinition {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "alpha4", "bases", "experiment.cbse.terministic.de_simulationexperiments.yaml"))
	if err != nil {
		t.Fatalf("read isolated alpha4 CRD: %v", err)
	}
	crd := &extensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(contents, crd); err != nil {
		t.Fatalf("decode isolated alpha4 CRD: %v", err)
	}
	return crd
}

func requiredProperty(t *testing.T, schema *extensionsv1.JSONSchemaProps, name string) *extensionsv1.JSONSchemaProps {
	t.Helper()
	property, ok := schema.Properties[name]
	if !ok {
		t.Fatalf("schema property %q is absent", name)
	}
	return &property
}

func requireSchemaRequired(t *testing.T, schema *extensionsv1.JSONSchemaProps, fields ...string) {
	t.Helper()
	required := make(map[string]struct{}, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := required[field]; !ok {
			t.Errorf("schema field %q is not required", field)
		}
	}
}

func sortedPropertyNames(schema *extensionsv1.JSONSchemaProps) []string {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	if len(names) == 2 && names[0] > names[1] {
		names[0], names[1] = names[1], names[0]
	}
	return names
}

func hasValidation(schema *extensionsv1.JSONSchemaProps, rule, messageFragment string) bool {
	for _, validation := range schema.XValidations {
		if validation.Rule == rule && strings.Contains(validation.Message, messageFragment) {
			return true
		}
	}
	return false
}

func assertNoPreserveUnknownFields(t *testing.T, schema *extensionsv1.JSONSchemaProps, path string) {
	t.Helper()
	if schema.XPreserveUnknownFields != nil && *schema.XPreserveUnknownFields {
		t.Errorf("%s enables x-kubernetes-preserve-unknown-fields", path)
	}
	for name, property := range schema.Properties {
		propertyCopy := property
		assertNoPreserveUnknownFields(t, &propertyCopy, path+"."+name)
	}
	if schema.Items != nil && schema.Items.Schema != nil {
		assertNoPreserveUnknownFields(t, schema.Items.Schema, path+"[]")
	}
	if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
		assertNoPreserveUnknownFields(t, schema.AdditionalProperties.Schema, path+".*")
	}
}

func localEnvtestAssets() string {
	if os.Getenv("KUBEBUILDER_ASSETS") != "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "bin", "k8s"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			assets, err := filepath.Abs(filepath.Join("..", "..", "bin", "k8s", entry.Name()))
			if err == nil {
				return assets
			}
		}
	}
	return ""
}
