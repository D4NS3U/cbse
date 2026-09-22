#!/usr/bin/env bash
# Shared source-image lock loader for the CBSE test harness.
#
# test/e2e/images.lock.env pins the four locked source images (BuildKit,
# Python, PostgreSQL, Go builder) and their provenance versions. They are
# immutable repository inputs with no environment override: this function
# reads the file verbatim, rejects duplicates and unknown keys, rejects any
# externally supplied value for the same key, and validates that each image
# entry is <name>@sha256:<64-lowercase-hex> with no tag. On success the eight
# lock variables (BUILDER_IMAGE, BUILDER_VERSION, PYTHON_BASE_IMAGE,
# PYTHON_BASE_VERSION, POSTGRES_IMAGE, POSTGRES_VERSION,
# TRANSLATOR_GO_BUILDER_IMAGE, TRANSLATOR_GO_VERSION) are set in the caller's
# shell. The function returns non-zero on any validation failure.
load_image_lock() {
  local lock_file="$1"
  local key value line s
  local -a seen=()
  for key in BUILDER_IMAGE BUILDER_VERSION PYTHON_BASE_IMAGE PYTHON_BASE_VERSION \
            POSTGRES_IMAGE POSTGRES_VERSION TRANSLATOR_GO_BUILDER_IMAGE TRANSLATOR_GO_VERSION; do
    [[ -z "${!key:-}" ]] || { echo "locked source image ${key} must not be overridden via the environment" >&2; return 1; }
  done
  [[ -r "${lock_file}" ]] || { echo "image lock is not readable: ${lock_file}" >&2; return 1; }
  while IFS= read -r line || [[ -n "${line}" ]]; do
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    [[ "${line}" == *=* ]] || { echo "malformed image lock line: ${line}" >&2; return 1; }
    key="${line%%=*}"
    value="${line#*=}"
    [[ -n "${key}" ]] || { echo "image lock has an empty key: ${line}" >&2; return 1; }
    for s in ${seen[@]+"${seen[@]}"}; do
      [[ "${s}" != "${key}" ]] || { echo "duplicate image lock key: ${key}" >&2; return 1; }
    done
    seen+=("${key}")
    case "${key}" in
      BUILDER_IMAGE|BUILDER_VERSION|PYTHON_BASE_IMAGE|PYTHON_BASE_VERSION|POSTGRES_IMAGE|POSTGRES_VERSION|TRANSLATOR_GO_BUILDER_IMAGE|TRANSLATOR_GO_VERSION)
        printf -v "${key}" '%s' "${value}"
        ;;
      *)
        echo "unknown image lock key: ${key}" >&2; return 1
        ;;
    esac
  done <"${lock_file}"
  for key in BUILDER_IMAGE PYTHON_BASE_IMAGE POSTGRES_IMAGE TRANSLATOR_GO_BUILDER_IMAGE; do
    [[ "${!key}" =~ ^[^@:]+@sha256:[0-9a-f]{64}$ ]] || {
      echo "${key} must be <name>@sha256:<64-lowercase-hex> with no tag: ${!key}" >&2
      return 1
    }
  done
}
