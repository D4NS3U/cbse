package registry

import (
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateDigestImage(t *testing.T) {
	valid := []string{
		"registry.example.com/path/image@sha256:" + strings.Repeat("a", 64),
		"image@sha256:" + strings.Repeat("0", 64),
	}
	for _, ref := range valid {
		if err := ValidateDigestImage(ref); err != nil {
			t.Fatalf("valid %q: %v", ref, err)
		}
	}
	invalid := []string{
		"",
		"image:tag",
		"image@sha256:tooshort",
		"image@sha256:" + strings.Repeat("A", 64), // uppercase
		"image@sha256:" + strings.Repeat("g", 64), // non-hex
		"image",
	}
	for _, ref := range invalid {
		if err := ValidateDigestImage(ref); err == nil {
			t.Fatalf("invalid %q: want error", ref)
		}
	}
}

func TestValidateRepository(t *testing.T) {
	valid := []string{
		"registry.unibw.de/i31bdase/cbse-test-runner",
		"registry.example.com:5000/team/image",
		"image",
	}
	for _, repo := range valid {
		if err := ValidateRepository(repo); err != nil {
			t.Fatalf("valid %q: %v", repo, err)
		}
	}
	invalid := []string{
		"",
		"registry.example.com/image:tag",
		"image@sha256:" + strings.Repeat("a", 64),
		"registry:5000/image:tag",
	}
	for _, repo := range invalid {
		if err := ValidateRepository(repo); err == nil {
			t.Fatalf("invalid %q: want error", repo)
		}
	}
}

func TestRepositoryFromDigest(t *testing.T) {
	ref := "registry.example.com/team/runner@sha256:" + strings.Repeat("a", 64)
	repo, err := RepositoryFromDigest(ref)
	if err != nil {
		t.Fatalf("repository from digest: %v", err)
	}
	if repo != "registry.example.com/team/runner" {
		t.Fatalf("repo = %q", repo)
	}
	// A non-digest reference is rejected.
	if _, err := RepositoryFromDigest("image:tag"); err == nil {
		t.Fatal("non-digest reference: want error")
	}
}

func TestRegistryAuthority(t *testing.T) {
	cases := map[string]string{
		"registry.example.com/team/image@sha256:" + strings.Repeat("a", 64): "registry.example.com",
		"registry.example.com:5000/team/image":                              "registry.example.com:5000",
		"localhost:5000/image":                                              "localhost:5000",
		"docker.io/library/image":                                           "docker.io",
		"library/image":                                                     "docker.io",
		"image":                                                             "docker.io",
	}
	for ref, want := range cases {
		got, err := RegistryAuthority(ref)
		if err != nil {
			t.Fatalf("authority %q: %v", ref, err)
		}
		if got != want {
			t.Fatalf("authority %q = %q; want %q", ref, got, want)
		}
	}
}

func TestValidateRegistrySecret(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	cfg := `{"auths":{"registry.example.com":{"auth":"` + auth + `"}}}`
	// Valid secret.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: RegistryAuthSecretName, Namespace: "ns"},
		Type:       RegistryAuthSecretType,
		Data:       map[string][]byte{DockerConfigSecretKey: []byte(cfg)},
	}
	raw, err := ValidateRegistrySecret(secret)
	if err != nil {
		t.Fatalf("valid secret: %v", err)
	}
	if u, p, err := ResolveDockerAuth(raw, "registry.example.com"); err != nil || u != "user" || p != "pass" {
		t.Fatalf("resolve: u=%q p=%q err=%v", u, p, err)
	}
	// Wrong type.
	bad := secret.DeepCopy()
	bad.Type = corev1.SecretType("Opaque")
	if _, err := ValidateRegistrySecret(bad); err == nil {
		t.Fatal("wrong type: want error")
	}
	// Missing key.
	bad = secret.DeepCopy()
	delete(bad.Data, DockerConfigSecretKey)
	if _, err := ValidateRegistrySecret(bad); err == nil {
		t.Fatal("missing key: want error")
	}
	// Malformed JSON.
	bad = secret.DeepCopy()
	bad.Data[DockerConfigSecretKey] = []byte("not json")
	if _, err := ValidateRegistrySecret(bad); err == nil {
		t.Fatal("malformed json: want error")
	}
	// Nil secret.
	if _, err := ValidateRegistrySecret(nil); err == nil {
		t.Fatal("nil secret: want error")
	}
}

func TestResolveDockerAuthAliasesAndRejections(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
	// docker.io alias resolution.
	cfg := `{"auths":{"https://index.docker.io/v1/":{"auth":"` + auth + `"}}}`
	if u, p, err := ResolveDockerAuth([]byte(cfg), "docker.io"); err != nil || u != "u" || p != "p" {
		t.Fatalf("docker.io alias: u=%q p=%q err=%v", u, p, err)
	}
	// No basic-auth entry.
	if _, _, err := ResolveDockerAuth([]byte(`{"auths":{}}`), "registry.example.com"); err == nil {
		t.Fatal("missing entry: want error")
	}
	// Credential-helper-only entry is unsupported.
	if _, _, err := ResolveDockerAuth([]byte(`{"auths":{},"credsStore":"desktop"}`), "registry.example.com"); err == nil {
		t.Fatal("credsStore-only: want error")
	}
	// Identity-token-only entry is unsupported.
	tok := `{"auths":{"registry.example.com":{"identitytoken":"abc"}}}`
	if _, _, err := ResolveDockerAuth([]byte(tok), "registry.example.com"); err == nil {
		t.Fatal("identity token: want error")
	}
	// Malformed base64.
	bad := `{"auths":{"registry.example.com":{"auth":"not-base64!!!"}}}`
	if _, _, err := ResolveDockerAuth([]byte(bad), "registry.example.com"); err == nil {
		t.Fatal("bad base64: want error")
	}
}
