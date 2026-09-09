package registryauth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadFileAndParse(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("robot:token"))
	p := writeConfig(t, `{"auths":{"registry.example.com":{"auth":"`+auth+`"}}}`)
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	u, pw, err := cfg.ResolveBasicAuth("registry.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u != "robot" || pw != "token" {
		t.Fatalf("u/pw = %q/%q", u, pw)
	}
}

func TestResolveBasicAuthExplicitUserPassword(t *testing.T) {
	p := writeConfig(t, `{"auths":{"gcr.io":{"username":"x","password":"y"}}}`)
	cfg, _ := LoadFile(p)
	u, pw, err := cfg.ResolveBasicAuth("gcr.io")
	if err != nil || u != "x" || pw != "y" {
		t.Fatalf("u/pw/err = %q/%q/%v", u, pw, err)
	}
}

func TestResolveBasicAuthHttpsAlias(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
	p := writeConfig(t, `{"auths":{"https://registry.example.com/v1/":{"auth":"`+auth+`"}}}`)
	cfg, _ := LoadFile(p)
	if u, _, err := cfg.ResolveBasicAuth("registry.example.com"); err != nil || u != "u" {
		t.Fatalf("https alias: u=%q err=%v", u, err)
	}
}

func TestResolveBasicAuthDockerIoAliases(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
	for _, key := range []string{"https://index.docker.io/v1/", "registry-1.docker.io"} {
		t.Run(key, func(t *testing.T) {
			p := writeConfig(t, `{"auths":{"`+key+`":{"auth":"`+auth+`"}}}`)
			cfg, _ := LoadFile(p)
			if u, _, err := cfg.ResolveBasicAuth("docker.io"); err != nil || u != "u" {
				t.Fatalf("docker.io alias %s: u=%q err=%v", key, u, err)
			}
		})
	}
}

func TestResolveBasicAuthRejectsMissing(t *testing.T) {
	p := writeConfig(t, `{"auths":{"other.example.com":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("a:b"))+`"}}}`)
	cfg, _ := LoadFile(p)
	if _, _, err := cfg.ResolveBasicAuth("registry.example.com"); err == nil {
		t.Fatal("missing authority must be rejected")
	}
}

func TestResolveBasicAuthRejectsIdentityToken(t *testing.T) {
	p := writeConfig(t, `{"auths":{"registry.example.com":{"identitytoken":"tok"}}}`)
	cfg, _ := LoadFile(p)
	if _, _, err := cfg.ResolveBasicAuth("registry.example.com"); err == nil {
		t.Fatal("identity-token entry must be rejected")
	}
}

func TestResolveBasicAuthRejectsCredHelperOnly(t *testing.T) {
	p := writeConfig(t, `{"credsStore":"desktop"}`)
	cfg, _ := LoadFile(p)
	if _, _, err := cfg.ResolveBasicAuth("registry.example.com"); err == nil {
		t.Fatal("credential-helper-only must be rejected")
	}
}

func TestAuthority(t *testing.T) {
	cases := map[string]string{
		"registry.example.com/proj/img":          "registry.example.com",
		"registry.example.com:5000/proj/img":     "registry.example.com:5000",
		"localhost:5000/img":                     "localhost:5000",
		"proj/img":                               "docker.io",
		"img":                                    "docker.io",
		"registry.example.com/proj/img@sha256:x": "registry.example.com",
	}
	for ref, want := range cases {
		got, err := Authority(ref)
		if err != nil {
			t.Fatalf("Authority(%q): %v", ref, err)
		}
		if got != want {
			t.Errorf("Authority(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestAuthorityRejectsEmpty(t *testing.T) {
	if _, err := Authority(""); err == nil {
		t.Fatal("empty reference must be rejected")
	}
	if _, err := Authority("@sha256:abc"); err == nil {
		t.Fatal("digest-only reference must be rejected")
	}
}
