package controller

import (
	"encoding/base64"
	"strings"
	"testing"

	experimentalpha4 "github.com/D4NS3U/cbse/experiment-operator/api/alpha4"
	corev1 "k8s.io/api/core/v1"
	kresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateDigestImage(t *testing.T) {
	valid := "registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + strings.Repeat("a", 64)
	if err := ValidateDigestImage(valid); err != nil {
		t.Fatalf("valid digest ref rejected: %v", err)
	}
	bad := []string{
		"registry.unibw.de/i31bdase/cbse-test/translator:latest",
		"registry.unibw.de/i31bdase/cbse-test/translator@sha256:short",
		"registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + strings.Repeat("A", 64), // uppercase hex
		"registry.unibw.de/i31bdase/cbse-test/translator",
		"",
	}
	for _, ref := range bad {
		if err := ValidateDigestImage(ref); err == nil {
			t.Fatalf("ValidateDigestImage(%q) expected error", ref)
		}
	}
}

func TestValidateRepository(t *testing.T) {
	if err := ValidateRepository("registry.unibw.de/i31bdase/cbse-test-runner"); err != nil {
		t.Fatalf("valid repository rejected: %v", err)
	}
	bad := []string{
		"registry.unibw.de/i31bdase/cbse-test-runner:v1",
		"registry.unibw.de/i31bdase/cbse-test-runner@sha256:" + strings.Repeat("a", 64),
		"nginx:1.2",
		"",
	}
	for _, repo := range bad {
		if err := ValidateRepository(repo); err == nil {
			t.Fatalf("ValidateRepository(%q) expected error", repo)
		}
	}
}

func TestRegistryAuthority(t *testing.T) {
	tests := map[string]string{
		"registry.unibw.de/i31bdase/cbse-test-runner":                                       "registry.unibw.de",
		"registry.unibw.de/i31bdase/cbse-test/translator@sha256:" + strings.Repeat("a", 64): "registry.unibw.de",
		"localhost:5000/my/image":                                                           "localhost:5000",
		"nginx":                                                                             "docker.io",
		"library/postgres":                                                                  "docker.io",
		"docker.io/library/postgres":                                                        "docker.io",
	}
	for ref, want := range tests {
		got, err := RegistryAuthority(ref)
		if err != nil {
			t.Fatalf("RegistryAuthority(%q) err = %v", ref, err)
		}
		if got != want {
			t.Fatalf("RegistryAuthority(%q) = %q, want %q", ref, got, want)
		}
	}
}

func b64Auth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func TestResolveDockerAuth(t *testing.T) {
	auth := b64Auth("robot", "secret")
	// Standard alias set exercised by the preflight jq resolver.
	cfg := `{"auths":{"https://registry.unibw.de":{"auth":"` + auth + `"},"https://index.docker.io/v1/":{"auth":"` + b64Auth("dock", "hub") + `"}}}`
	type tc struct {
		name      string
		authority string
		wantUser  string
		wantErr   string
	}
	cases := []tc{
		{"bare host alias", "registry.unibw.de", "robot", ""},
		{"https host alias", "registry.unibw.de", "robot", ""}, // same config, different candidate
		{"docker.io index alias", "docker.io", "dock", ""},
		{"missing authority", "quay.io", "", "no basic-auth entry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, p, err := ResolveDockerAuth([]byte(cfg), c.authority)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("ResolveDockerAuth(%q) err = %v, want contains %q", c.authority, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveDockerAuth(%q) err = %v", c.authority, err)
			}
			if u != c.wantUser || p == "" {
				t.Fatalf("ResolveDockerAuth(%q) = (%q, %q), want user %q with non-empty password", c.authority, u, p, c.wantUser)
			}
		})
	}
}

func TestResolveDockerAuthRejectsTokensAndHelpers(t *testing.T) {
	tokenOnly := `{"auths":{"https://registry.unibw.de":{"identitytoken":"tok"}}}`
	if _, _, err := ResolveDockerAuth([]byte(tokenOnly), "registry.unibw.de"); err == nil ||
		!strings.Contains(err.Error(), "identity tokens are unsupported") {
		t.Fatalf("token-only entry should be rejected, got %v", err)
	}
	helperOnly := `{"auths":{},"credsStore":"osxkeychain"}`
	if _, _, err := ResolveDockerAuth([]byte(helperOnly), "registry.unibw.de"); err == nil ||
		!strings.Contains(err.Error(), "credential-helper-only") {
		t.Fatalf("credential-helper-only entry should be rejected, got %v", err)
	}
	// Missing entry with no helpers and no unauthenticated fallback.
	missing := `{"auths":{"https://quay.io":{"auth":"` + b64Auth("q", "p") + `"}}}`
	if _, _, err := ResolveDockerAuth([]byte(missing), "registry.unibw.de"); err == nil ||
		!strings.Contains(err.Error(), "no basic-auth entry") {
		t.Fatalf("missing entry should be rejected without unauthenticated fallback, got %v", err)
	}
}

func TestValidateRegistrySecret(t *testing.T) {
	valid := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: registryAuthSecretName, Namespace: "ns"},
		Type:       registryAuthSecretType,
		Data:       map[string][]byte{dockerConfigSecretKey: []byte(`{"auths":{"https://registry.unibw.de":{"auth":"` + b64Auth("u", "p") + `"}}}`)},
	}
	if _, err := ValidateRegistrySecret(valid); err != nil {
		t.Fatalf("valid secret rejected: %v", err)
	}
	if _, err := ValidateRegistrySecret(nil); err == nil {
		t.Fatal("nil secret should be rejected")
	}
	wrongType := valid.DeepCopy()
	wrongType.Type = corev1.SecretTypeOpaque
	if _, err := ValidateRegistrySecret(wrongType); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("wrong type should be rejected, got %v", err)
	}
	missingKey := valid.DeepCopy()
	delete(missingKey.Data, dockerConfigSecretKey)
	if _, err := ValidateRegistrySecret(missingKey); err == nil || !strings.Contains(err.Error(), ".dockerconfigjson") {
		t.Fatalf("missing key should be rejected, got %v", err)
	}
	malformed := valid.DeepCopy()
	malformed.Data[dockerConfigSecretKey] = []byte("not json")
	if _, err := ValidateRegistrySecret(malformed); err == nil {
		t.Fatal("malformed JSON should be rejected")
	}
}

func TestEffectiveBuilderResources(t *testing.T) {
	t.Run("nil defaults cpu and mem limits", func(t *testing.T) {
		got, err := EffectiveBuilderResources(nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Limits.Cpu().Cmp(kresource.MustParse("1")) != 0 {
			t.Fatalf("default cpu limit = %v, want 1", got.Limits.Cpu())
		}
		if got.Limits.Memory().Cmp(kresource.MustParse("2Gi")) != 0 {
			t.Fatalf("default mem limit = %v, want 2Gi", got.Limits.Memory())
		}
		if len(got.Requests) != 0 {
			t.Fatalf("requests should default absent, got %v", got.Requests)
		}
	})
	t.Run("supplied limits replace defaults independently", func(t *testing.T) {
		req := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: kresource.MustParse("4Gi")},
		}
		got, err := EffectiveBuilderResources(req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Limits.Cpu().Cmp(kresource.MustParse("1")) != 0 {
			t.Fatalf("cpu limit should default to 1, got %v", got.Limits.Cpu())
		}
		if got.Limits.Memory().Cmp(kresource.MustParse("4Gi")) != 0 {
			t.Fatalf("mem limit should be supplied 4Gi, got %v", got.Limits.Memory())
		}
	})
	t.Run("request not exceeding limit accepted", func(t *testing.T) {
		req := &corev1.ResourceRequirements{
			Limits:   corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("2")},
			Requests: corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("500m")},
		}
		if _, err := EffectiveBuilderResources(req); err != nil {
			t.Fatalf("valid request rejected: %v", err)
		}
	})
	t.Run("request exceeding limit rejected", func(t *testing.T) {
		req := &corev1.ResourceRequirements{
			Limits:   corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("1")},
			Requests: corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("2")},
		}
		if _, err := EffectiveBuilderResources(req); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("request exceeding limit should be rejected, got %v", err)
		}
	})
	t.Run("non cpu/mem rejected", func(t *testing.T) {
		req := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{"hugepages-2Mi": kresource.MustParse("2Mi")},
		}
		if _, err := EffectiveBuilderResources(req); err == nil || !strings.Contains(err.Error(), "not cpu or memory") {
			t.Fatalf("hugepages should be rejected, got %v", err)
		}
	})
	t.Run("claims rejected", func(t *testing.T) {
		req := &corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "c"}}}
		if _, err := EffectiveBuilderResources(req); err == nil || !strings.Contains(err.Error(), "claims") {
			t.Fatalf("claims should be rejected, got %v", err)
		}
	})
	t.Run("non-positive rejected", func(t *testing.T) {
		req := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceCPU: kresource.MustParse("0")},
		}
		if _, err := EffectiveBuilderResources(req); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Fatalf("zero limit should be rejected, got %v", err)
		}
	})
}

func validDigest(name string) string {
	return name + "@sha256:" + strings.Repeat("a", 64)
}

func TestValidateDatabaseSpec(t *testing.T) {
	// A baseline valid image-form spec; variants override individual fields.
	base := func() experimentalpha4.DatabaseSpec {
		return experimentalpha4.DatabaseSpec{
			Image:  "registry.unibw.de/i31bdase/cbse-test/db:latest",
			DBName: "d", User: "u", Password: "p", Port: 5432,
		}
	}
	host := func(hostName string) experimentalpha4.DatabaseSpec {
		s := base()
		s.Image = ""
		s.Host = hostName
		return s
	}
	accept := []struct {
		name string
		spec experimentalpha4.DatabaseSpec
	}{
		{"image form", base()},
		{"host dns", host("db.example.com")},
		{"host ipv4", host("10.0.0.1")},
		{"host ipv6", host("2001:db8::1")},
	}
	for _, c := range accept {
		t.Run("accept/"+c.name, func(t *testing.T) {
			if err := validateDatabaseSpec(c.spec, "db"); err != nil {
				t.Fatalf("expected accept, got %v", err)
			}
		})
	}
	reject := []struct {
		name string
		spec experimentalpha4.DatabaseSpec
	}{
		{"neither", func() experimentalpha4.DatabaseSpec { s := base(); s.Image = ""; return s }()},
		{"both", func() experimentalpha4.DatabaseSpec { s := base(); s.Host = "db.example.com"; return s }()},
		{"blank dbname", func() experimentalpha4.DatabaseSpec { s := base(); s.DBName = ""; return s }()},
		{"blank user", func() experimentalpha4.DatabaseSpec { s := base(); s.User = ""; return s }()},
		{"blank password", func() experimentalpha4.DatabaseSpec { s := base(); s.Password = ""; return s }()},
		{"port zero", func() experimentalpha4.DatabaseSpec { s := base(); s.Port = 0; return s }()},
		{"port too large", func() experimentalpha4.DatabaseSpec { s := base(); s.Port = 70000; return s }()},
		{"host embedded port", func() experimentalpha4.DatabaseSpec { s := host("db.example.com:5432"); return s }()},
		{"host url", func() experimentalpha4.DatabaseSpec { s := host("https://db.example.com"); return s }()},
		{"host bracketed ipv6", func() experimentalpha4.DatabaseSpec { s := host("[2001:db8::1]"); return s }()},
		{"host zone ipv6", func() experimentalpha4.DatabaseSpec { s := host("fe80::1%eth0"); return s }()},
		{"host uppercase dns", func() experimentalpha4.DatabaseSpec { s := host("DB.Example.com"); return s }()},
		{"host with command", func() experimentalpha4.DatabaseSpec { s := host("db.example.com"); s.Command = []string{"x"}; return s }()},
		{"host with args", func() experimentalpha4.DatabaseSpec { s := host("db.example.com"); s.Args = []string{"x"}; return s }()},
		{"host with nodePort", func() experimentalpha4.DatabaseSpec {
			s := host("db.example.com")
			n := int32(30000)
			s.NodePort = &n
			return s
		}()},
		{"host with NodePort serviceType", func() experimentalpha4.DatabaseSpec {
			s := host("db.example.com")
			s.ServiceType = experimentalpha4.ServiceTypeNodePort
			return s
		}()},
	}
	for _, c := range reject {
		t.Run("reject/"+c.name, func(t *testing.T) {
			if err := validateDatabaseSpec(c.spec, "db"); err == nil {
				t.Fatalf("expected reject, got nil")
			}
		})
	}
}
