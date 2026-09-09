// Package imageref owns the deterministic runner image tag construction, the
// framework-owned OCI manifest identity annotations, and the post-push
// repository and annotation verification for the reference Translator.
//
// The pushed tag is <repository>:runner-<12-char-UID-prefix>-s<scenario-id>-a<attempt>,
// where the UID prefix is the first 12 lowercase hexadecimal characters of the
// experiment UID after removing hyphens. The BuildKit image exporter adds the
// three credential-free identity annotations experiment-uid, scenario-id, and
// translation-attempt. After a successful push, Translator resolves the tag,
// verifies the normalized repository and all three annotations exactly match
// the current request, and publishes only the digest reference.
package imageref

import (
	"fmt"
	"strings"
)

// AnnotationPrefix is the common prefix for the framework-owned identity
// annotations BuildKit adds to the pushed manifest.
const AnnotationPrefix = "experiment.cbse.terministic.de"

// Annotation keys added to the OCI manifest by the BuildKit image exporter.
const (
	AnnotationExperimentUID      = AnnotationPrefix + "/experiment-uid"
	AnnotationScenarioID         = AnnotationPrefix + "/scenario-id"
	AnnotationTranslationAttempt = AnnotationPrefix + "/translation-attempt"
)

// UIDPrefix returns the first 12 lowercase hexadecimal characters of uid after
// removing hyphens. It matches the canonical per-experiment prefix shared by
// the Translator durable consumer name and the runner image tag.
func UIDPrefix(uid string) string {
	stripped := strings.ToLower(strings.ReplaceAll(uid, "-", ""))
	if len(stripped) > 12 {
		stripped = stripped[:12]
	}
	return stripped
}

// Tag constructs the deterministic pushed runner tag:
// <repository>:runner-<uidPrefix>-s<scenarioID>-a<attempt>.
func Tag(repository, uidPrefix string, scenarioID, attempt int) string {
	return fmt.Sprintf("%s:runner-%s-s%d-a%d", repository, uidPrefix, scenarioID, attempt)
}

// Annotations returns the three credential-free OCI manifest identity
// annotations for the given experiment UID, scenario ID, and translation
// attempt.
func Annotations(uid string, scenarioID, attempt int) map[string]string {
	return map[string]string{
		AnnotationExperimentUID:      uid,
		AnnotationScenarioID:         fmt.Sprintf("%d", scenarioID),
		AnnotationTranslationAttempt: fmt.Sprintf("%d", attempt),
	}
}

// NormalizeRepository lowercases the repository for case-insensitive identity
// comparison. Registry repositories are conventionally lowercase; the push and
// verification paths normalize before comparing so a registry that returns a
// differently-cased repository is still recognized as the same image.
func NormalizeRepository(repo string) string {
	return strings.ToLower(repo)
}

// VerifyAnnotations reports whether all three identity annotations exactly
// match the given experiment UID, scenario ID, and translation attempt. A
// missing annotation is a mismatch. It does not check the repository; the
// caller verifies the repository separately.
func VerifyAnnotations(ann map[string]string, uid string, scenarioID, attempt int) error {
	want := Annotations(uid, scenarioID, attempt)
	for _, key := range []string{AnnotationExperimentUID, AnnotationScenarioID, AnnotationTranslationAttempt} {
		got, ok := ann[key]
		if !ok {
			return fmt.Errorf("missing manifest annotation %s", key)
		}
		if got != want[key] {
			return fmt.Errorf("manifest annotation %s=%q != %q", key, got, want[key])
		}
	}
	return nil
}

// DigestRef constructs the digest-only reference published in a success ready
// message: <repository>@<digest>.
func DigestRef(repository, digest string) string {
	return repository + "@" + digest
}

// SplitRepositoryTag splits a pushed tag <repository>:<tag> into its repository
// and tag components. It rejects a digest reference or a tag missing a
// repository. It does not validate the registry host grammar.
func SplitRepositoryTag(ref string) (repo, tag string, err error) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return "", "", fmt.Errorf("reference %q is a digest reference, not a tag", ref)
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return "", "", fmt.Errorf("reference %q has no tag separator", ref)
	}
	if strings.ContainsAny(ref[i+1:], "/") {
		return "", "", fmt.Errorf("reference %q has an invalid tag", ref)
	}
	return ref[:i], ref[i+1:], nil
}

// RepositoryOf returns the repository component of a tag or digest reference,
// normalized to lowercase. For a digest reference <repo>@<digest> it returns
// the part before '@'; for a tag reference <repo>:<tag> it returns the part
// before the last ':'.
func RepositoryOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return NormalizeRepository(ref[:i])
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.ContainsAny(ref[i+1:], "/") {
		return NormalizeRepository(ref[:i])
	}
	return NormalizeRepository(ref)
}
