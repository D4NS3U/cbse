// Package registry resolves a pushed runner image tag to its digest and
// verifies the framework-owned OCI manifest identity annotations over the
// Docker distribution HTTP API.
//
// Translator pushes the deterministic tag with BuildKit, then resolves the tag
// to a digest and verifies the normalized repository and all three identity
// annotations (experiment-uid, scenario-id, translation-attempt) exactly match
// the current request before writing the success outcome. A registry not-found
// result permits generation and build; any other resolution failure creates an
// empty-failure outcome. Registry credentials come only from the mounted Docker
// configuration through registryauth; no credential is printed or annotated.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/D4NS3U/cbse/component-templates/translator/internal/registryauth"
)

// ErrNotFound is returned when the registry has no manifest for the tag. The
// orchestrator treats this as "no recoverable registry tag" and permits a fresh
// generation and build.
var ErrNotFound = errors.New("registry: tag not found")

// Manifest annotations are parsed from the manifest body's top-level
// "annotations" object (OCI image manifest). BuildKit's image exporter writes
// the three framework-owned identity annotations there.
type manifestBody struct {
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Resolver resolves a tag to its digest and manifest annotations.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (digest string, annotations map[string]string, err error)
}

// Client is the HTTP registry client. It uses an injected *http.Client so
// tests can supply a fake RoundTripper; production uses http.DefaultClient.
type Client struct {
	auth   *registryauth.Config
	client *http.Client
}

// NewClient returns a registry client that authenticates through the mounted
// Docker configuration (resolved by auth) and speaks HTTPS to remote hosts and
// plain HTTP only to loopback hosts.
func NewClient(auth *registryauth.Config, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{auth: auth, client: client}
}

// Resolve resolves ref (a tag reference <host>/<name>:<tag>) to its digest and
// manifest annotations. On a 404 it returns ErrNotFound.
func (c *Client) Resolve(ctx context.Context, ref string) (string, map[string]string, error) {
	host, name, refOrDigest, err := splitRef(ref)
	if err != nil {
		return "", nil, err
	}
	manifestURL := c.manifestURL(host, name, refOrDigest)
	body, hdr, err := c.getManifest(ctx, host, manifestURL)
	if err != nil {
		return "", nil, err
	}
	digest := hdr.Get("Docker-Content-Digest")
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	var mb manifestBody
	if err := json.Unmarshal(body, &mb); err != nil {
		return "", nil, fmt.Errorf("registry: parse manifest: %w", err)
	}
	return digest, mb.Annotations, nil
}

// VerifyAndResolve resolves the tag, verifies the normalized repository and all
// three identity annotations, and returns the digest. expectedRepo is the
// configured repository (e.g. registry.example.com/proj/runners).
func (c *Client) VerifyAndResolve(ctx context.Context, tagRef, expectedRepo, uid string, scenarioID, attempt int) (string, error) {
	gotRepo := repositoryOf(tagRef)
	if strings.ToLower(gotRepo) != strings.ToLower(expectedRepo) {
		return "", fmt.Errorf("registry: repository %q != %q", gotRepo, expectedRepo)
	}
	digest, ann, err := c.Resolve(ctx, tagRef)
	if err != nil {
		return "", err
	}
	if err := verifyAnnotations(ann, uid, scenarioID, attempt); err != nil {
		return "", err
	}
	return digest, nil
}

func verifyAnnotations(ann map[string]string, uid string, scenarioID, attempt int) error {
	const p = "experiment.cbse.terministic.de"
	want := map[string]string{
		p + "/experiment-uid":      uid,
		p + "/scenario-id":         fmt.Sprintf("%d", scenarioID),
		p + "/translation-attempt": fmt.Sprintf("%d", attempt),
	}
	for k, v := range want {
		got, ok := ann[k]
		if !ok {
			return fmt.Errorf("registry: missing manifest annotation %s", k)
		}
		if got != v {
			return fmt.Errorf("registry: annotation %s=%q != %q", k, got, v)
		}
	}
	return nil
}

// getManifest performs the manifest GET, handling 401 bearer/basic auth using
// the mounted Docker configuration credentials for host.
func (c *Client) getManifest(ctx context.Context, host, manifestURL string) ([]byte, http.Header, error) {
	body, hdr, status, err := c.do(ctx, manifestURL, "")
	if err != nil {
		return nil, nil, err
	}
	if status == http.StatusUnauthorized {
		authHdr := hdr.Get("WWW-Authenticate")
		if authHdr == "" {
			return nil, nil, fmt.Errorf("registry: 401 without WWW-Authenticate")
		}
		token, aerr := c.authorize(ctx, host, authHdr)
		if aerr != nil {
			return nil, nil, aerr
		}
		_ = body
		body, hdr, status, err = c.do(ctx, manifestURL, token)
		if err != nil {
			return nil, nil, err
		}
	}
	switch {
	case status == http.StatusNotFound:
		return nil, nil, ErrNotFound
	case status >= 400:
		return nil, nil, fmt.Errorf("registry: manifest GET status %d", status)
	}
	return body, hdr, nil
}

// authorize handles a WWW-Authenticate challenge: Bearer token exchange or
// Basic retry. It returns an Authorization header value (e.g. "Bearer <t>"
// or "Basic <b>").
func (c *Client) authorize(ctx context.Context, host, wwwAuth string) (string, error) {
	lower := strings.ToLower(wwwAuth)
	switch {
	case strings.HasPrefix(lower, "bearer"):
		return c.bearer(ctx, host, wwwAuth)
	case strings.HasPrefix(lower, "basic"):
		user, pass, err := c.basicCreds(host)
		if err != nil {
			return "", err
		}
		return "Basic " + basicAuth(user, pass), nil
	default:
		return "", fmt.Errorf("registry: unsupported WWW-Authenticate %q", wwwAuth)
	}
}

func (c *Client) bearer(ctx context.Context, host, wwwAuth string) (string, error) {
	realm, service, scope := parseBearerChallenge(wwwAuth)
	if realm == "" {
		return "", fmt.Errorf("registry: bearer challenge missing realm")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("registry: bearer realm: %w", err)
	}
	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if user, pass, err := c.basicCreds(host); err == nil && user != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(user, pass))
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("registry: token endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("registry: token endpoint status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("registry: read token: %w", err)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("registry: parse token: %w", err)
	}
	if tok.Token == "" {
		tok.Token = tok.AccessToken
	}
	if tok.Token == "" {
		return "", errors.New("registry: empty token")
	}
	return "Bearer " + tok.Token, nil
}

func (c *Client) basicCreds(host string) (string, string, error) {
	if c.auth == nil {
		return "", "", errors.New("registry: no auth resolver")
	}
	return c.auth.ResolveBasicAuth(host)
}

// basicAuth returns the base64 user:password value for a Basic header.
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

func (c *Client) do(ctx context.Context, manifestURL, authHeader string) ([]byte, http.Header, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.index.v1+json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("registry: manifest GET: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("registry: read manifest: %w", err)
	}
	return body, resp.Header, resp.StatusCode, nil
}

func (c *Client) manifestURL(host, name, ref string) string {
	scheme := "https"
	if isLoopback(host) {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, host, name, ref)
}

// splitRef splits <host>/<name>:<tag> or <host>/<name>@<digest> into host, name,
// and the tag/digest reference.
func splitRef(ref string) (host, name, refOrDigest string, err error) {
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return "", "", "", fmt.Errorf("registry: invalid reference %q", ref)
	}
	host = ref[:slash]
	rest := ref[slash+1:]
	if at := strings.Index(rest, "@"); at >= 0 {
		return host, rest[:at], rest[at+1:], nil
	}
	colon := strings.LastIndex(rest, ":")
	if colon < 0 {
		return "", "", "", fmt.Errorf("registry: reference %q has no tag", ref)
	}
	return host, rest[:colon], rest[colon+1:], nil
}

// repositoryOf returns the <host>/<name> part of a tag or digest reference.
func repositoryOf(ref string) string {
	if at := strings.Index(ref, "@"); at >= 0 {
		return ref[:at]
	}
	if slash := strings.Index(ref, "/"); slash >= 0 {
		rest := ref[slash+1:]
		if colon := strings.LastIndex(rest, ":"); colon >= 0 {
			return ref[:slash+1+colon]
		}
	}
	return ref
}

func isLoopback(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		h = h[:strings.Index(h, ":")]
	}
	return h == "localhost" || strings.HasPrefix(h, "127.")
}

// parseBearerChallenge extracts realm, service, and scope from a Bearer
// WWW-Authenticate header value.
func parseBearerChallenge(wwwAuth string) (realm, service, scope string) {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(wwwAuth, "Bearer"), "bearer"))
	for _, kv := range strings.Split(rest, ",") {
		kv = strings.TrimSpace(kv)
		if eq := strings.Index(kv, "="); eq >= 0 {
			k := strings.TrimSpace(kv[:eq])
			v := strings.Trim(strings.TrimSpace(kv[eq+1:]), `"`)
			switch k {
			case "realm":
				realm = v
			case "service":
				service = v
			case "scope":
				scope = v
			}
		}
	}
	return
}
