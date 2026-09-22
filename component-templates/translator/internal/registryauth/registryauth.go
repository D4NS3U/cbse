// Package registryauth ports the standard Docker config-file basic-auth resolver
// used by the CBSE Operator, so the reference Translator framework resolves
// registry credentials with identical semantics without depending on the
// Operator's internal package. The framework reads the registry-auth Secret
// mounted at /registry-auth/config.json and resolves non-empty basic auth
// (username, password) for a registry authority using the standard alias
// lookup and rejection rules.
//
// The resolver never falls back to unauthenticated access, even for a public
// registry, and rejects identity-token and credential-helper-only entries:
// only direct basic-auth (base64 username:password or explicit username and
// password) entries are supported. This keeps the framework's registry
// behavior identical to the Operator admission validation.
package registryauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config is the parsed subset of a Docker config.json the framework uses. Only
// the auths map and the credential-helper hints are parsed; extension fields
// are ignored.
type Config struct {
	Auths       map[string]AuthEntry `json:"auths"`
	CredsStore  string               `json:"credsStore,omitempty"`
	CredHelpers map[string]string    `json:"credHelpers,omitempty"`
}

// AuthEntry is one auths entry. Auth is base64(username:password); Username and
// Password are the unencoded equivalents; IdentityToken and RegistryToken are
// token-only credentials the framework does not support.
type AuthEntry struct {
	Auth          string `json:"auth,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	RegistryToken string `json:"registrytoken,omitempty"`
}

// LoadFile reads and parses a Docker config JSON file at path (typically
// /registry-auth/config.json, the mounted cbse-registry-auth Secret data key).
func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read docker config %q: %w", path, err)
	}
	return Parse(raw)
}

// Parse parses a Docker config JSON blob into a Config.
func Parse(raw []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse Docker config JSON: %w", err)
	}
	return &cfg, nil
}

// ResolveBasicAuth resolves non-empty basic-auth (username, password) for the
// given registry authority using standard Docker config-file resolver
// semantics, including the well-known docker.io aliases. Identity tokens and
// credential-helper-only entries are unsupported. The resolver never falls
// back to unauthenticated access.
func (c *Config) ResolveBasicAuth(authority string) (username, password string, err error) {
	if c == nil {
		return "", "", fmt.Errorf("registry %q: no basic-auth entry in Docker config", authority)
	}
	if c.Auths == nil {
		c.Auths = map[string]AuthEntry{}
	}
	for _, key := range authCandidates(authority) {
		entry, ok := c.Auths[key]
		if !ok {
			continue
		}
		u, p, err := basicAuthFromEntry(entry)
		if err != nil {
			return "", "", fmt.Errorf("registry %q: %w", authority, err)
		}
		return u, p, nil
	}
	if c.CredsStore != "" || (c.CredHelpers != nil && credHelpersCover(c.CredHelpers, authority)) {
		return "", "", fmt.Errorf("registry %q: credential-helper-only entries are unsupported", authority)
	}
	return "", "", fmt.Errorf("registry %q: no basic-auth entry in Docker config", authority)
}

// authCandidates returns the standard Docker config-file lookup keys for a
// registry authority, including the well-known docker.io aliases, mirroring
// the resolver semantics used by BuildKit and the Docker CLI.
func authCandidates(authority string) []string {
	a := strings.TrimSpace(authority)
	candidates := []string{
		a,
		"https://" + a,
		"https://" + a + "/v1/",
	}
	if a == "docker.io" {
		candidates = append(candidates,
			"https://index.docker.io/v1/",
			"https://registry-1.docker.io",
			"https://registry-1.docker.io/v1/",
			"registry-1.docker.io",
		)
	}
	return candidates
}

// credHelpersCover reports whether any credHelpers entry targets the authority
// or its docker.io alias.
func credHelpersCover(helpers map[string]string, authority string) bool {
	for host := range helpers {
		if host == authority || host == "https://"+authority || host == "https://"+authority+"/v1/" {
			return true
		}
		if authority == "docker.io" && (host == "https://index.docker.io/v1/" || host == "registry-1.docker.io") {
			return true
		}
	}
	return false
}

// basicAuthFromEntry extracts non-empty username and password basic auth from a
// single auths entry. It prefers the base64 auth field, then the explicit
// username/password fields. Token-only entries are rejected.
func basicAuthFromEntry(entry AuthEntry) (string, string, error) {
	if entry.Auth != "" {
		decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return "", "", errors.New("auth field is not valid base64")
		}
		user, pass, ok := strings.Cut(string(decoded), ":")
		if !ok || user == "" || pass == "" {
			return "", "", errors.New("auth field does not encode username:password")
		}
		return user, pass, nil
	}
	if entry.Username != "" && entry.Password != "" {
		return entry.Username, entry.Password, nil
	}
	if entry.IdentityToken != "" || entry.RegistryToken != "" {
		return "", "", errors.New("identity tokens are unsupported")
	}
	return "", "", errors.New("entry has no basic credentials")
}

// Authority parses the registry authority (host[:port]) from an image reference
// or repository path, following standard Docker reference parsing. A name
// without an explicit registry host resolves to docker.io. This mirrors the
// Operator's RegistryAuthority so the framework resolves the same authorities.
func Authority(ref string) (string, error) {
	name := ref
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[:i]
	}
	if strings.TrimSpace(name) == "" {
		return "", errors.New("registry authority cannot be parsed from an empty reference")
	}
	parts := strings.SplitN(name, "/", 2)
	first := parts[0]
	if len(parts) == 1 {
		return "docker.io", nil
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first, nil
	}
	return "docker.io", nil
}
