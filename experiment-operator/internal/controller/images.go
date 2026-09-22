package controller

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// digestRefRe matches the exact alpha4 OCI digest reference form
// name@sha256:<64 lowercase hexadecimal characters>. The name carries a
// repository path (and optional registry port) but no tag.
var digestRefRe = regexp.MustCompile(`^([^@\s]+)@sha256:[0-9a-f]{64}$`)

// ValidateDigestImage rejects anything other than the alpha4 digest reference
// form name@sha256:<64 lowercase hex>. translator.image, translator.baseimage,
// and translator.builderImage must satisfy this rule; tags alone are invalid.
func ValidateDigestImage(ref string) error {
	if !digestRefRe.MatchString(ref) {
		return fmt.Errorf("image %q must be an OCI digest reference in the exact form name@sha256:<64 lowercase hex>", ref)
	}
	return nil
}

// ValidateRepository rejects a translator.repository that carries a tag or a
// digest. A repository is a plain push path such as
// registry.unibw.de/i31bdase/cbse-test-runner; a trailing :tag or @digest is a
// configuration error.
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

// ValidateDatabaseImage accepts any non-empty image reference for an
// image-based database. The alpha4 digest requirement is limited to the
// Translator, Builder, and base images; a database image may be tagged or
// digested and is pulled with cbse-registry-auth either way.
func ValidateDatabaseImage(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return errors.New("database image is required")
	}
	if strings.ContainsAny(ref, " \t\r\n") {
		return fmt.Errorf("database image %q must not contain whitespace", ref)
	}
	return nil
}

// RegistryAuthority parses the registry authority (host[:port]) from an image
// reference or repository path, following standard Docker reference parsing.
// A name without an explicit registry host (a single segment, or a first
// segment that is not a host) resolves to docker.io.
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
