#!/usr/bin/env bash
# registry-cleanup.sh — annotation-verified generated-runner artifact cleanup.
#
# Targets ONLY the generated-runner repository (i31bdase/cbse-test-runner). It
# never deletes or prunes cbse-test, cbse-test/scenario-detail-database, or any
# shared Operator/SM/Builder/base/PostgreSQL/EDS/Translator image. It uses only
# the already-required curl and jq (no separate registry CLI or credential
# store), authenticates with the protected basic credentials resolved from the
# Docker configuration, and verifies every candidate through Harbor's
# artifact-by-digest API before deleting.
#
# A candidate is safe to delete only when all three framework OCI identity
# annotations (experiment-uid, scenario-id, translation-attempt) match the
# values encoded by its exact expected deterministic runner tag, and its
# complete attached tag set contains only that one exact tag. Any missing or
# mismatched annotation, or any extra attached tag, fails cleanup for that
# candidate without issuing a DELETE. Deletion is by digest; Harbor removes
# every tag attached to that digest, which is why the complete tag set is
# inspected first. Absence is idempotent: a candidate whose tag and digest are
# both already absent counts as success without a DELETE.
#
# Credential material is never printed; failures are reported without request
# headers, tokens, credential values, or credential paths.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

api_base="${CBSE_HARBOR_API:-https://registry.unibw.de/api/v2.0}"
project="${CBSE_HARBOR_PROJECT:-i31bdase}"
repo="${CBSE_RUNNER_REPO:-cbse-test-runner}"
auth_file="${CBSE_REGISTRY_AUTH_FILE:?CBSE_REGISTRY_AUTH_FILE is required}"
uid="${CBSE_EXPERIMENT_UID:?CBSE_EXPERIMENT_UID is required}"
records_file="${CBSE_RUNNER_RECORDS:-}"
fail_log="${CBSE_CLEANUP_FAIL_LOG:-}"

command -v curl >/dev/null || { echo "curl is required" >&2; exit 2; }
command -v jq >/dev/null   || { echo "jq is required" >&2; exit 2; }

# Safety: the target repository is fixed to the generated-runner repository.
# This adapter must never target cbse-test, cbse-test/scenario-detail-database,
# or any shared image repository.
case "${repo}" in
  ""|cbse-test|cbse-test/scenario-detail-database)
    echo "registry-cleanup: refusing unsafe target repository '${repo}'" >&2
    exit 2 ;;
esac

api_host="${api_base#https://}"
api_host="${api_host%%/*}"

# Resolve Harbor basic credentials for the API host from the Docker config.
auth_b64="$(jq -r --arg h "${api_host}" '.auths[$h].auth // empty' "${auth_file}" 2>/dev/null || true)"
if [[ -z "${auth_b64}" ]]; then
  # Fall back to username/password fields encoded as base64.
  user="$(jq -r --arg h "${api_host}" '.auths[$h].username // empty' "${auth_file}" 2>/dev/null || true)"
  pass="$(jq -r --arg h "${api_host}" '.auths[$h].password // empty' "${auth_file}" 2>/dev/null || true)"
  if [[ -n "${user}" && -n "${pass}" ]]; then
    auth_b64="$(printf '%s:%s' "${user}" "${pass}" | base64 | tr -d '\n')"
  fi
fi
[[ -n "${auth_b64}" ]] || { echo "registry-cleanup: no Harbor credentials for ${api_host}" >&2; exit 2; }

auth_header="Authorization: Basic ${auth_b64}"
api_path="projects/${project}/repositories/${repo}/artifacts"

# UID prefix: first 12 lowercase hex chars of the UID after removing hyphens.
uid_prefix="$(printf '%s' "${uid}" | tr -d -- '-' | tr '[:upper:]' '[:lower:]' | cut -c1-12)"
tag_prefix="runner-${uid_prefix}-s"

# fail logs a cleanup message to stderr and, when CBSE_CLEANUP_FAIL_LOG is
# set, appends it to that file so per-candidate failures are captured for
# triage. It never prints credentials or request headers.
fail() {
  echo "registry-cleanup: $*" >&2
  if [[ -n "${fail_log}" ]]; then echo "registry-cleanup: $*" >>"${fail_log}"; fi
}

# harbor_get <ref-or-empty> -> sets globals: hg_status, hg_body
harbor_get() {
  local ref="$1" path="${api_path}"
  [[ -z "${ref}" ]] || path="${api_path}/${ref}"
  local tmp
  tmp="$(mktemp)"
  hg_status="$(curl -sS -o "${tmp}" -w '%{http_code}' -H "${auth_header}" "${api_base}/${path}" 2>/dev/null || true)"
  hg_body="$(cat "${tmp}" 2>/dev/null || true)"
  rm -f "${tmp}"
}

# harbor_delete issues a Harbor DELETE for the given artifact digest and
# returns the HTTP status code. Deletion is by digest so Harbor removes every
# tag attached to that digest, which is why verify_and_delete inspects the
# complete tag set before calling this.
harbor_delete() {
  local digest="$1"
  curl -sS -o /dev/null -w '%{http_code}' -X DELETE -H "${auth_header}" \
    "${api_base}/${api_path}/${digest}" 2>/dev/null || true
}

# parse_tag <tag> -> sets globals: pt_scenario, pt_attempt
parse_tag() {
  local tag="$1"
  if [[ "${tag}" =~ ^runner-${uid_prefix}-s([0-9]+)-a([0-9]+)$ ]]; then
    pt_scenario="${BASH_REMATCH[1]}"
    pt_attempt="${BASH_REMATCH[2]}"
    return 0
  fi
  return 1
}

# expected_tag <scenario> <attempt>
expected_tag() { printf 'runner-%s-s%s-a%s' "${uid_prefix}" "$1" "$2"; }

# verify_and_delete <ref> <is_tag:0|1>
# Returns 0 on cleaned/already-absent, 1 on unsafe/failed.
verify_and_delete() {
  local ref="$1" is_tag="$2"
  local digest scenario attempt exp_tag

  # Determine the expected tag and encoded scenario/attempt.
  if [[ "${is_tag}" == 1 ]]; then
    if ! parse_tag "${ref}"; then
      fail "candidate tag '${ref}' does not match the deterministic runner format; skipping"
      return 1
    fi
    scenario="${pt_scenario}"; attempt="${pt_attempt}"
    exp_tag="$(expected_tag "${scenario}" "${attempt}")"
    [[ "${ref}" == "${exp_tag}" ]] || { fail "candidate tag '${ref}' != expected '${exp_tag}'"; return 1; }
  else
    exp_tag=""
  fi

  harbor_get "${ref}"
  local status="${hg_status}" body="${hg_body}"

  # Absent reference.
  if [[ "${status}" == "404" ]]; then
    # If we still have the other reference (tag vs digest), inspect it.
    if [[ "${is_tag}" == 1 ]]; then
      # Tag absent: nothing to delete for this candidate.
      return 0
    fi
    # Digest absent: idempotent success.
    return 0
  fi
  [[ "${status}" == "200" ]] || { fail "artifact read for '${ref}' returned HTTP ${status}; skipping"; return 1; }

  digest="$(printf '%s' "${body}" | jq -r '.digest // empty' 2>/dev/null || true)"
  [[ -n "${digest}" ]] || { fail "artifact '${ref}' has no digest; skipping"; return 1; }

  # For a digest reference, derive the expected tag from the annotations.
  if [[ "${is_tag}" == 0 ]]; then
    scenario="$(printf '%s' "${body}" | jq -r '.annotations["experiment.cbse.terministic.de/scenario-id"] // empty' 2>/dev/null || true)"
    attempt="$(printf '%s' "${body}" | jq -r '.annotations["experiment.cbse.terministic.de/translation-attempt"] // empty' 2>/dev/null || true)"
    if [[ -z "${scenario}" || -z "${attempt}" ]]; then
      fail "artifact '${ref}' is missing scenario/attempt annotations; skipping"
      return 1
    fi
    exp_tag="$(expected_tag "${scenario}" "${attempt}")"
  fi

  # Verify all three identity annotations.
  local got_uid got_sid got_att
  got_uid="$(printf '%s' "${body}" | jq -r '.annotations["experiment.cbse.terministic.de/experiment-uid"] // empty' 2>/dev/null || true)"
  got_sid="$(printf '%s' "${body}" | jq -r '.annotations["experiment.cbse.terministic.de/scenario-id"] // empty' 2>/dev/null || true)"
  got_att="$(printf '%s' "${body}" | jq -r '.annotations["experiment.cbse.terministic.de/translation-attempt"] // empty' 2>/dev/null || true)"
  [[ "${got_uid}" == "${uid}" ]]       || { fail "artifact '${ref}' experiment-uid mismatch; skipping"; return 1; }
  [[ "${got_sid}" == "${scenario}" ]]  || { fail "artifact '${ref}' scenario-id mismatch; skipping"; return 1; }
  [[ "${got_att}" == "${attempt}" ]]  || { fail "artifact '${ref}' translation-attempt mismatch; skipping"; return 1; }

  # Complete tag-set check: only the exact expected tag may be attached.
  local tag_count extra
  tag_count="$(printf '%s' "${body}" | jq -r '[.tags[]?.name] | length' 2>/dev/null || echo 0)"
  extra="$(printf '%s' "${body}" | jq -r --arg t "${exp_tag}" '[.tags[]?.name] | map(select(. != $t)) | first // empty' 2>/dev/null || true)"
  if [[ "${tag_count}" -gt 1 || -n "${extra}" ]]; then
    fail "artifact '${ref}' has an unexpected attached tag; skipping (would delete shared tag)"
    return 1
  fi
  # Require the exact expected tag to be present in the set.
  local has_exp
  has_exp="$(printf '%s' "${body}" | jq -r --arg t "${exp_tag}" '[.tags[]?.name] | any(. == $t)' 2>/dev/null || echo false)"
  [[ "${has_exp}" == "true" ]] || { fail "artifact '${ref}' is not tagged '${exp_tag}'; skipping"; return 1; }

  # Delete by digest.
  local del_status
  del_status="$(harbor_delete "${digest}")"
  [[ "${del_status}" == "200" || "${del_status}" == "404" ]] || {
    fail "delete of digest ${digest} returned HTTP ${del_status}"
    return 1
  }

  # Verify absence: both the tag and digest must be absent after deletion.
  harbor_get "${exp_tag}"
  [[ "${hg_status}" == "404" ]] || { fail "tag '${exp_tag}' still resolves after delete (HTTP ${hg_status})"; return 1; }
  harbor_get "${digest}"
  [[ "${hg_status}" == "404" ]] || { fail "digest ${digest} still resolves after delete (HTTP ${hg_status})"; return 1; }
  return 0
}

# Collect candidates: recorded tags/digests plus discovered prefix matches.
declare -a refs=()
declare -a is_tags=()
# add_candidate records a candidate artifact reference (a tag or a digest)
# for cleanup, skipping empty values and deduping against already-recorded
# refs. It classifies the reference into is_tags (1 for a tag, 0 for a sha256
# digest) so verify_and_delete knows how to derive the expected tag.
add_candidate() {
  local r="$1"
  [[ -n "${r}" ]] || return 0
  local k
  for k in "${refs[@]+"${refs[@]}"}"; do [[ "${k}" != "${r}" ]] || return 0; done
  refs+=("${r}")
  if [[ "${r}" == sha256:* || "${r}" == *@sha256:* ]]; then is_tags+=("0"); else is_tags+=("1"); fi
}

# Recorded candidates.
if [[ -n "${records_file}" && -f "${records_file}" ]]; then
  while IFS= read -r line; do add_candidate "${line}"; done < "${records_file}"
fi

# Discovered candidates via paginated artifact list (filter by tag prefix).
page=1
while :; do
  harbor_get "?page_size=100&page=${page}"
  if [[ "${hg_status}" == "404" ]]; then break; fi   # repository absent -> nothing to discover
  [[ "${hg_status}" == "200" ]] || { fail "artifact list page ${page} returned HTTP ${hg_status}"; break; }
  count="$(printf '%s' "${hg_body}" | jq -r '(.items // .) | length' 2>/dev/null || echo 0)"
  if [[ "${count}" -eq 0 ]]; then break; fi
  # Discover the digest of every artifact that carries a deterministic runner
  # tag matching this experiment's UID prefix.
  while IFS= read -r dref; do add_candidate "${dref}"; done < <(
    printf '%s' "${hg_body}" | jq -r --arg p "${tag_prefix}" '
      (.items // .)[] | select((.tags // []) | any(.name | startswith($p))) | .digest
    ' 2>/dev/null || true
  )
  [[ "${count}" -lt 100 ]] && break
  page=$((page + 1))
done

# Attempt every candidate; a failure does not abort the remaining candidates.
rc=0
i=0
for ref in "${refs[@]+"${refs[@]}"}"; do
  if ! verify_and_delete "${ref}" "${is_tags[$i]}"; then rc=1; fi
  i=$((i + 1))
done

exit "${rc}"
