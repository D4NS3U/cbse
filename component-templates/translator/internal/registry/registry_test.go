package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth"
)

type fakeRegistry struct {
	srv       *httptest.Server
	cfg       *registryauth.Config
	manifest  []byte
	digest    string
	wwwAuth   string
	status    int
	basicAuth bool
}

func newFakeRegistry(t *testing.T, manifest []byte, digest string) *fakeRegistry {
	f := &fakeRegistry{manifest: manifest, digest: digest, status: http.StatusOK}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/token"):
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if f.wwwAuth != "" && r.Header.Get("Authorization") == "" {
				w.Header().Set("WWW-Authenticate", f.wwwAuth)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if f.status == http.StatusNotFound {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Docker-Content-Digest", f.digest)
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.WriteHeader(f.status)
			_, _ = w.Write(f.manifest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	f.srv = srv
	host := strings.TrimPrefix(srv.URL, "http://")
	f.cfg = &registryauth.Config{
		Auths: map[string]registryauth.AuthEntry{
			host: {Auth: basicAuth("user", "pass")},
		},
	}
	return f
}

func (f *fakeRegistry) bearerChallenge() {
	f.wwwAuth = `Bearer realm="` + f.srv.URL + `/token",service="` + f.host() + `",scope="repository:test:pull"`
}

func (f *fakeRegistry) basicChallenge() {
	f.wwwAuth = `Basic realm="r"`
}

func (f *fakeRegistry) host() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func manifestWithAnnotations(ann map[string]string) []byte {
	b, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"annotations":   ann,
	})
	return b
}

func TestResolveBearerThenVerify(t *testing.T) {
	ann := map[string]string{
		"experiment.cbse.terministic.de/experiment-uid":      "uid-123",
		"experiment.cbse.terministic.de/scenario-id":         "7",
		"experiment.cbse.terministic.de/translation-attempt": "2",
	}
	f := newFakeRegistry(t, manifestWithAnnotations(ann), "sha256:abcd")
	f.bearerChallenge()
	cl := NewClient(f.cfg, f.srv.Client())
	ref := f.host() + "/test:runner-x-s7-a2"
	digest, gotAnn, err := cl.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:abcd" {
		t.Fatalf("digest = %q", digest)
	}
	if gotAnn["experiment.cbse.terministic.de/scenario-id"] != "7" {
		t.Fatalf("annotations = %v", gotAnn)
	}
	if _, err := cl.VerifyAndResolve(context.Background(), ref, f.host()+"/test", "uid-123", 7, 2); err != nil {
		t.Fatalf("VerifyAndResolve: %v", err)
	}
	if _, err := cl.VerifyAndResolve(context.Background(), ref, f.host()+"/test", "uid-123", 8, 2); err == nil {
		t.Fatal("mismatched scenario must fail")
	}
	if _, err := cl.VerifyAndResolve(context.Background(), ref, "other/repo", "uid-123", 7, 2); err == nil {
		t.Fatal("mismatched repo must fail")
	}
}

func TestResolveNotFound(t *testing.T) {
	f := newFakeRegistry(t, nil, "")
	f.status = http.StatusNotFound
	cl := NewClient(f.cfg, f.srv.Client())
	ref := f.host() + "/test:runner-x-s1-a1"
	_, _, err := cl.Resolve(context.Background(), ref)
	if err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveBasicAuth(t *testing.T) {
	ann := map[string]string{
		"experiment.cbse.terministic.de/experiment-uid":      "u",
		"experiment.cbse.terministic.de/scenario-id":         "1",
		"experiment.cbse.terministic.de/translation-attempt": "1",
	}
	f := newFakeRegistry(t, manifestWithAnnotations(ann), "sha256:zz")
	f.basicChallenge()
	cl := NewClient(f.cfg, f.srv.Client())
	ref := f.host() + "/test:runner-x-s1-a1"
	digest, _, err := cl.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:zz" {
		t.Fatalf("digest = %q", digest)
	}
}

func TestSplitRefAndRepositoryOf(t *testing.T) {
	host, name, ref, err := splitRef("registry.example.com/proj/runners:runner-x-s1-a1")
	if err != nil || host != "registry.example.com" || name != "proj/runners" || ref != "runner-x-s1-a1" {
		t.Fatalf("splitRef = %q %q %q %v", host, name, ref, err)
	}
	if repositoryOf("registry.example.com/proj/runners:runner-x-s1-a1") != "registry.example.com/proj/runners" {
		t.Fatal("repositoryOf(tag)")
	}
	if repositoryOf("registry.example.com/proj/runners@sha256:abc") != "registry.example.com/proj/runners" {
		t.Fatal("repositoryOf(digest)")
	}
}
