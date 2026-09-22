// Package registry owns the alpha4 image-reference and Docker-config resolver
// used by the Scenario Manager runner-start and observation workflows. It is a
// faithful cross-module copy of the Experiment Operator's
// internal/controller/images.go and registryauth.go so SM revalidates the
// persisted runner digest and the cbse-registry-auth Secret with the same
// standard resolver semantics as the Operator, without importing the
// Operator's internal package (a separate Go module whose internal/ tree is
// not importable).
//
// The two copies are pinned to the same contract: the Operator validates and
// persists the immutable template; SM revalidates the persisted digest and the
// live Secret at runner-start time to catch a Secret replaced after
// provisioning. A drift between the two copies is a contract change that must
// be applied to both together.
package registry

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// RegistryAuthSecretName is the fixed, namespace-local Docker configuration
// Secret name referenced by the alpha4 SimulationExperiment and read by SM
// with a get-only, resourceNames-scoped RBAC grant.
const RegistryAuthSecretName = "cbse-registry-auth"

// DockerConfigSecretKey is the single data key on a dockerconfigjson Secret.
const DockerConfigSecretKey = ".dockerconfigjson"

// RegistryAuthSecretType is the required Secret type.
const RegistryAuthSecretType = corev1.SecretType("kubernetes.io/dockerconfigjson")

// digestRefRe matches the exact alpha4 OCI digest reference form
// name@sha256:<64 lowercase hexadecimal characters>.
var digestRefRe = regexp.MustCompile(`^([^@\s]+)@sha256:[0-9a-f]{64}$`)

// dockerConfig mirrors the subset of the Docker config.json structure SM
// validates. Only the auths map and the credential-helper hints are parsed.
type dockerConfig struct {
	Auths       map[string]dockerAuthEntry `json:"auths"`
	CredsStore  string                     `json:"credsStore,omitempty"`
	CredHelpers map[string]string          `json:"credHelpers,omitempty"`
}

// dockerAuthEntry is one auths entry. Auth is base64(username:password);
// Username and Password are the unencoded equivalents; IdentityToken and
// RegistryToken are token-only credentials SM does not support.
type dockerAuthEntry struct {
	Auth          string `json:"auth,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	RegistryToken string `json:"registrytoken,omitempty"`
}

// ValidateDigestImage rejects anything other than the alpha4 digest reference
// form name@sha256:<64 lowercase hex>. The persisted runner digest
// (scenario_status.container_image) must satisfy this rule; a tag or bare name
// is an invalid runner image.
func ValidateDigestImage(ref string) error {
	if !digestRefRe.MatchString(ref) {
		return fmt.Errorf("image %q must be an OCI digest reference in the exact form name@sha256:<64 lowercase hex>", ref)
	}
	return nil
}

// ValidateRepository rejects a translator.repository that carries a tag or a
// digest. A repository is a plain push path; a trailing :tag or @digest is a
// configuration error. SM requires the normalized repository of the persisted
// runner digest to equal spec.translator.repository.
func ValidateRepository(repo string) error {
	if strings.TrimSpace(repo) == "" {
		return errors.New("translator.repository is required")
	}
	if strings.Contains(repo, "@") {
		return fmt.Errorf("translator.repository %q must not carry a digest", repo)
	}
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		last := repo[i+1:]
		if strings.Contains(last, ":") {
			return fmt.Errorf("translator.repository %q must not carry a tag", repo)
		}
	} else if strings.Contains(repo, ":") {
		return fmt.Errorf("translator.repository %q must not carry a tag", repo)
	}
	return nil
}

// RepositoryFromDigest extracts the repository path from a digest reference
// name@sha256:... by stripping the @digest suffix. It rejects a reference that
// is not a digest reference. The returned repository is compared to
// spec.translator.repository after normalization.
func RepositoryFromDigest(ref string) (string, error) {
	if err := ValidateDigestImage(ref); err != nil {
		return "", err
	}
	name := ref
	if i := strings.LastIndex(name, "@"); i >= 0 {
		name = name[:i]
	}
	return name, nil
}

// RegistryAuthority parses the registry authority (host[:port]) from an image
// reference or repository path, following standard Docker reference parsing. A
// name without an explicit registry host resolves to docker.io.
func RegistryAuthority(ref string) (string, error) {
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

// dockerAuthCandidates returns the standard Docker config-file lookup keys for
// a registry authority, including the well-known docker.io aliases.
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
// registry aliases. Identity tokens and credential-helper-only entries are
// unsupported. The resolver never falls back to unauthenticated access.
func ResolveDockerAuth(dockerconfigjson []byte, authority string) (username, password string, err error) {
	var cfg dockerConfig
	if err := json.Unmarshal(dockerconfigjson, &cfg); err != nil {
		return "", "", fmt.Errorf("parse Docker config JSON: %w", err)
	}
	return cfg.resolveAuth(authority)
}

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
	if c.CredsStore != "" || (c.CredHelpers != nil && credHelpersCover(c.CredHelpers, authority)) {
		return "", "", fmt.Errorf("registry %q: credential-helper-only entries are unsupported", authority)
	}
	return "", "", fmt.Errorf("registry %q: no basic-auth entry in Docker config", authority)
}

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
// the raw config bytes so callers can resolve authorities without re-parsing.
func ValidateRegistrySecret(secret *corev1.Secret) ([]byte, error) {
	if secret == nil {
		return nil, fmt.Errorf("registry Secret %q is missing", RegistryAuthSecretName)
	}
	if secret.Type != RegistryAuthSecretType {
		return nil, fmt.Errorf("registry Secret %q has type %q, want %q", RegistryAuthSecretName, secret.Type, RegistryAuthSecretType)
	}
	raw, ok := secret.Data[DockerConfigSecretKey]
	if !ok {
		return nil, fmt.Errorf("registry Secret %q is missing the %q key", RegistryAuthSecretName, DockerConfigSecretKey)
	}
	var cfg dockerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("registry Secret %q .dockerconfigjson is not valid JSON: %w", RegistryAuthSecretName, err)
	}
	return raw, nil
}
