package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// dockerConfig mirrors the subset of the Docker config.json structure the
// Operator validates. Only the auths map and the credential-helper hints are
// parsed; extension fields are ignored.
type dockerConfig struct {
	Auths       map[string]dockerAuthEntry `json:"auths"`
	CredsStore  string                     `json:"credsStore,omitempty"`
	CredHelpers map[string]string          `json:"credHelpers,omitempty"`
}

// dockerAuthEntry is one auths entry. Auth is base64(username:password);
// Username and Password are the unencoded equivalents; IdentityToken and
// RegistryToken are token-only credentials the Operator does not support.
type dockerAuthEntry struct {
	Auth          string `json:"auth,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	RegistryToken string `json:"registrytoken,omitempty"`
}

const registryAuthSecretName = "cbse-registry-auth"

// dockerConfigSecretKey is the single data key on a dockerconfigjson Secret.
const dockerConfigSecretKey = ".dockerconfigjson"

// registryAuthSecretType is the required Secret type.
const registryAuthSecretType = corev1.SecretType("kubernetes.io/dockerconfigjson")

// dockerAuthCandidates returns the standard Docker config-file lookup keys for
// a registry authority, including the well-known docker.io aliases, mirroring
// the resolver semantics used by BuildKit and the Docker CLI.
func dockerAuthCandidates(authority string) []string {
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

// ResolveDockerAuth parses a Docker config JSON blob and resolves non-empty
// basic-auth (username, password) for the given registry authority using
// standard Docker config-file resolver semantics, including well-known
// registry aliases.
//
// Identity tokens and credential-helper-only entries are unsupported: an
// auths entry that carries only an identity or registry token, or the absence
// of any direct auths entry when credsStore/credHelpers would otherwise
// supply credentials, returns an error. The resolver never falls back to
// unauthenticated access, even for a public registry.
func ResolveDockerAuth(dockerconfigjson []byte, authority string) (username, password string, err error) {
	var cfg dockerConfig
	if err := json.Unmarshal(dockerconfigjson, &cfg); err != nil {
		return "", "", fmt.Errorf("parse Docker config JSON: %w", err)
	}
	return cfg.resolveAuth(authority)
}

// resolveAuth resolves non-empty basic-auth for the given authority from an
// already-parsed config, sharing the standard alias lookup and rejection
// rules with ResolveDockerAuth without re-parsing raw bytes.
func (c *dockerConfig) resolveAuth(authority string) (string, string, error) {
	if c.Auths == nil {
		c.Auths = map[string]dockerAuthEntry{}
	}
	for _, key := range dockerAuthCandidates(authority) {
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
	// No direct basic-auth entry. Distinguish credential-helper-only from a
	// plainly missing entry so the Operator reports a descriptive error.
	if c.CredsStore != "" || (c.CredHelpers != nil && credHelpersCover(c.CredHelpers, authority)) {
		return "", "", fmt.Errorf("registry %q: credential-helper-only entries are unsupported", authority)
	}
	return "", "", fmt.Errorf("registry %q: no basic-auth entry in Docker config", authority)
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
func basicAuthFromEntry(entry dockerAuthEntry) (string, string, error) {
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

// ValidateRegistrySecret loads and validates the cbse-registry-auth Secret for
// basic-auth use. It requires the dockerconfigjson type and a
// .dockerconfigjson key containing valid Docker configuration JSON. It returns
// the parsed config so callers can resolve authorities without re-parsing.
func ValidateRegistrySecret(secret *corev1.Secret) (*dockerConfig, error) {
	if secret == nil {
		return nil, fmt.Errorf("registry Secret %q is missing", registryAuthSecretName)
	}
	if secret.Type != registryAuthSecretType {
		return nil, fmt.Errorf("registry Secret %q has type %q, want %q", registryAuthSecretName, secret.Type, registryAuthSecretType)
	}
	raw, ok := secret.Data[dockerConfigSecretKey]
	if !ok {
		return nil, fmt.Errorf("registry Secret %q is missing the %q key", registryAuthSecretName, dockerConfigSecretKey)
	}
	var cfg dockerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("registry Secret %q .dockerconfigjson is not valid JSON: %w", registryAuthSecretName, err)
	}
	return &cfg, nil
}
