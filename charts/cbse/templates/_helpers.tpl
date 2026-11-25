{{- /*
CBSE chart helper templates.

Every helper serves exactly one pinned invariant from the H1 chart contract
(devlog/changes/helm-control-plane-installation/slices/H1-chart-skeleton-contract-tests.md):
stable labels, digest-only image references, the fixed registry pull-Secret
name, and the fixed compatibility names that injected endpoints and lookup
logic depend on. There is deliberately no generic abstraction framework and
no release-prefixed variant of any fixed name.
*/ -}}

{{- /*
cbse.selectorLabels — stable selector labels for chart-owned workloads.
Selectors contain only stable keys and never the chart version.
*/ -}}
{{- define "cbse.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- /*
cbse.labels — full standard label set for chart-owned objects: the selector
labels plus version, managed-by, and part-of.
*/ -}}
{{- define "cbse.labels" -}}
{{ include "cbse.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
{{- end }}

{{- /*
cbse.image — composes an immutable image reference from one image object.
Requires both a non-empty repository and a non-empty digest, validates the
digest against sha256:<64 lowercase hex>, rejects tag-bearing repositories,
and emits <repository>@<digest>. It never accepts a tag.
*/ -}}
{{- define "cbse.image" -}}
{{- $repo := required "image.repository is required; supply repositories and digests through release values; the chart source ships no private defaults" (index . "repository") -}}
{{- $digest := required "image.digest is required; image references are immutable digest-pinned and tag-based references are not supported" (index . "digest") -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) }}
{{- fail "the image digest must match sha256:<64 lowercase hex characters>" }}
{{- end }}
{{- if contains ":" (last (splitList "/" $repo)) }}
{{- fail "the image repository must not carry a tag; only repository@sha256-digest references are supported" }}
{{- end }}
{{- printf "%s@%s" $repo $digest }}
{{- end }}

{{- /*
cbse.serviceAccountImagePullSecrets — emits exactly the fixed registry pull
Secret name cbse-registry-auth, which the installation owner manages and the
chart only references.
*/ -}}
{{- define "cbse.serviceAccountImagePullSecrets" -}}
- name: cbse-registry-auth
{{- end }}

{{- /*
Fixed compatibility names. These return exactly the pinned runtime names;
they are never release-prefixed and must not change.
*/ -}}
{{- define "cbse.name.experimentOperator" -}}
experiment-operator
{{- end }}
{{- define "cbse.name.scenarioManager" -}}
scenario-manager
{{- end }}
{{- define "cbse.name.natsService" -}}
sm-eds-nats
{{- end }}
{{- define "cbse.name.coreDatabaseService" -}}
core-db
{{- end }}
{{- define "cbse.name.coreDatabaseCredentials" -}}
core-db-credentials
{{- end }}
