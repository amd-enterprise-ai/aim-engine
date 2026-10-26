#!/usr/bin/env bash
set -euo pipefail

DEFAULT_REGISTRY="docker.io"
DEFAULT_NAMESPACE="amdenterpriseai"
DEFAULT_FILTER="aim-*"
DEFAULT_SECRET_NAME="ghcr-pull-secret"
DEFAULT_SECRET_OUTPUT="secret.yaml"
DEFAULT_MODELSOURCE_NAME="aim-discovered"
DEFAULT_MODELSOURCE_OUTPUT="aimclustermodelsource.yaml"
DEFAULT_JOBS=10
# AIMClusterModelSource.spec.images has +kubebuilder:validation:MaxItems=100
MODELSOURCE_MAX_IMAGES=100

usage() {
  cat <<USAGE
List container images from a registry namespace by glob pattern.

Useful for finding images matching a pattern (e.g. "*qwen*", "*epyc*").
Can also generate an AIMClusterModelSource manifest directly with -M.

Output: one image reference per line on stdout; status on stderr.
By default only repos matching "${DEFAULT_FILTER}" are listed; use --all for everything.

Usage:
  $(basename "$0") [OPTIONS] [REGISTRY/NAMESPACE]
  $(basename "$0") -h

Options:
  -c, --config FILE          Path to a .dockerconfigjson file for authentication
  -f, --filter PATTERN       Filter to apply (default: ${DEFAULT_FILTER}). May be repeated;
                             a repo or tag is emitted if ANY filter accepts it.
                             PATTERN may be "repo-glob" or "repo-glob:tag-glob"
                             e.g. "aim-*", "aim-*:0.9", "*qwen*"
  -a, --all                  Match every repo and tag (resets any prior -f).
  -j, --jobs N               Max parallel tag fetches (default: ${DEFAULT_JOBS})
  -S, --create-secret [NAME] Write a Kubernetes docker-registry Secret YAML
                             (requires --config, default name: ${DEFAULT_SECRET_NAME})
  -o, --secret-output FILE   Where to write the Secret YAML (default: ${DEFAULT_SECRET_OUTPUT})
  -M, --modelsource [NAME]   Write an AIMClusterModelSource YAML wrapping the
                             discovered image list (default name: ${DEFAULT_MODELSOURCE_NAME}).
                             Refuses to write more than ${MODELSOURCE_MAX_IMAGES} images (CRD cap on
                             spec.images). Auto-references the --create-secret name if set.
      --modelsource-output FILE
                             Where to write the AIMClusterModelSource YAML
                             (default: ${DEFAULT_MODELSOURCE_OUTPUT})
  -n, --namespace NS         Kubernetes namespace for the secret (default: default)
  -h, --help                 Show this help message

Arguments:
  REGISTRY/NAMESPACE    Registry and namespace to list (default: ${DEFAULT_REGISTRY}/${DEFAULT_NAMESPACE})
                        A bare namespace (no slash) is treated as docker.io/NAMESPACE.
                        e.g. docker.io/amdenterpriseai  or  ghcr.io/silogen

Examples:
  # List default AIM images from docker.io/amdenterpriseai
  $(basename "$0")

  # All repos and tags in a namespace
  $(basename "$0") --all amdenterpriseai

  # Authenticated listing from GHCR
  $(basename "$0") -c dockerconfig.json ghcr.io/silogen

  # Filter by repo and tag glob
  $(basename "$0") -f "aim-*:0.9" docker.io/amdenterpriseai

  # Combine multiple patterns (union)
  $(basename "$0") -f "*qwen*" -f "*deepseek*"

  # Save an image list and a matching ImagePullSecret, then apply the secret
  $(basename "$0") -c creds.json -S ghcr-pull-secret -o secret.yaml ghcr.io/silogen > images.txt
  kubectl apply -f secret.yaml

  # One-shot: discover images, generate the AIMClusterModelSource + ImagePullSecret
  # (the operator looks up the secret in its own namespace; use -n to match)
  $(basename "$0") -c creds.json -S ghcr-pull-secret -n aim-system \
    -M ghcr-aim-models ghcr.io/silogen
  kubectl apply -f secret.yaml -f aimclustermodelsource.yaml
USAGE
}

config_file=""
filters=()
all_flag=false
target=""
create_secret=""
secret_namespace="default"
secret_output="$DEFAULT_SECRET_OUTPUT"
max_parallel="$DEFAULT_JOBS"
create_modelsource=""
modelsource_output="$DEFAULT_MODELSOURCE_OUTPUT"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--config)         config_file="$2"; shift 2 ;;
    -f|--filter)         filters+=("$2"); shift 2 ;;
    -a|--all)            all_flag=true; filters=(); shift ;;
    -j|--jobs)           max_parallel="$2"; shift 2 ;;
    -S|--create-secret)
      if [[ $# -ge 2 && "$2" != -* ]]; then
        create_secret="$2"; shift 2
      else
        create_secret="$DEFAULT_SECRET_NAME"; shift
      fi
      ;;
    -o|--secret-output)  secret_output="$2"; shift 2 ;;
    -M|--modelsource)
      if [[ $# -ge 2 && "$2" != -* ]]; then
        create_modelsource="$2"; shift 2
      else
        create_modelsource="$DEFAULT_MODELSOURCE_NAME"; shift
      fi
      ;;
    --modelsource-output) modelsource_output="$2"; shift 2 ;;
    -n|--namespace)      secret_namespace="$2"; shift 2 ;;
    -h|--help)           usage; exit 0 ;;
    -*)                  echo "Error: unknown option: $1" >&2; echo >&2; usage >&2; exit 1 ;;
    *)                   target="$1"; shift ;;
  esac
done

if ! [[ "$max_parallel" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: --jobs must be a positive integer (got: ${max_parallel})" >&2
  exit 1
fi

# An empty filters array means "match everything". Apply the default only when
# the user supplied neither -f nor -a, so -a's "match all" intent is preserved.
if [[ "$all_flag" != true && ${#filters[@]} -eq 0 ]]; then
  filters=("$DEFAULT_FILTER")
fi

# AIMClusterModelSource is cluster-scoped but resolves imagePullSecrets from the
# AIM operator's own namespace, so the Secret produced by -S must live there too.
# Warn (don't auto-pick) when the user combines -S with -M but leaves -n at "default".
if [[ -n "$create_secret" && -n "$create_modelsource" && "$secret_namespace" == "default" ]]; then
  cat >&2 <<'EOF'
Warning: -S will write the Secret in namespace 'default', but AIMClusterModelSource
         resolves imagePullSecrets from the AIM operator's own namespace (typically
         'aim-system'). Re-run with '-n <operator-namespace>' to align the two,
         or move the Secret yourself after applying.
EOF
fi

if [[ -z "$target" ]]; then
  target="${DEFAULT_REGISTRY}/${DEFAULT_NAMESPACE}"
fi

registry="${target%%/*}"
namespace="${target#*/}"

# Bare namespace (no slash) assumes docker.io
if [[ "$registry" == "$namespace" ]]; then
  namespace="$registry"
  registry="$DEFAULT_REGISTRY"
fi

# --config is only consumed by GHCR token extraction and by --create-secret.
# Warn if the user supplied it against docker.io with no --create-secret, since
# Docker Hub API authentication is not implemented yet and the file is unused.
if [[ -n "$config_file" && "$registry" == "docker.io" && -z "$create_secret" ]]; then
  echo "Warning: --config is unused for docker.io API calls (Hub auth not implemented);" >&2
  echo "         the file is only consumed by --create-secret. Drop -c or add -S." >&2
fi

# ---- Create Secret YAML ----

if [[ -n "$create_secret" ]]; then
  if [[ -z "$config_file" ]]; then
    echo "Error: --create-secret requires --config" >&2
    exit 1
  fi
  if [[ ! -f "$config_file" ]]; then
    echo "Error: config file not found: $config_file" >&2
    exit 1
  fi
  encoded=$(base64 -w0 < "$config_file")
  cat > "$secret_output" <<EOF
---
apiVersion: v1
kind: Secret
metadata:
  name: ${create_secret}
  namespace: ${secret_namespace}
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: ${encoded}
EOF
  echo "# Secret YAML for '${create_secret}' written to ${secret_output}" >&2
fi

# ---- Filter matching ----

matches_glob() {
  local pattern="$1" string="$2"
  # shellcheck disable=SC2053  # glob match requires unquoted RHS
  [[ "$string" == $pattern ]]
}

# A repo matches if any filter's repo-part matches.
# Empty filters array means "match everything" (-a/--all).
repo_matches() {
  local repo="$1"
  if [[ ${#filters[@]} -eq 0 ]]; then
    return 0
  fi
  local f
  for f in "${filters[@]}"; do
    if matches_glob "${f%%:*}" "$repo"; then
      return 0
    fi
  done
  return 1
}

# A (repo, tag) pair matches if any filter accepts both: its repo-part matches
# the repo, AND it has no tag-part OR its tag-part matches the tag.
tag_matches() {
  local repo="$1" tag="$2"
  if [[ ${#filters[@]} -eq 0 ]]; then
    return 0
  fi
  local f repo_part tag_part
  for f in "${filters[@]}"; do
    repo_part="${f%%:*}"
    if [[ "$f" == *:* ]]; then
      tag_part="${f#*:}"
    else
      tag_part=""
    fi
    if matches_glob "$repo_part" "$repo" \
        && { [[ -z "$tag_part" ]] || matches_glob "$tag_part" "$tag"; }; then
      return 0
    fi
  done
  return 1
}

# ---- Docker Hub ----

list_dockerhub_repos() {
  local ns="$1"
  local url="https://hub.docker.com/v2/namespaces/${ns}/repositories?page_size=100"
  while [[ -n "$url" && "$url" != "null" ]]; do
    local resp
    if ! resp=$(curl -sf "$url"); then
      echo "Error: failed to fetch Docker Hub repositories for '${ns}' (url: ${url})" >&2
      return 1
    fi
    echo "$resp" | jq -r '.results[].name'
    url=$(echo "$resp" | jq -r '.next // empty')
  done
}

list_dockerhub_tags() {
  local repo="$1"
  local url="https://hub.docker.com/v2/repositories/${repo}/tags?page_size=100"
  while [[ -n "$url" && "$url" != "null" ]]; do
    local resp
    if ! resp=$(curl -sf "$url"); then
      return 1
    fi
    echo "$resp" | jq -r '.results[].name'
    url=$(echo "$resp" | jq -r '.next // empty')
  done
}

# ---- GHCR ----

extract_ghcr_token() {
  if [[ -z "$config_file" ]]; then
    echo "Error: GHCR requires --config with ghcr.io credentials" >&2
    exit 1
  fi
  if [[ ! -f "$config_file" ]]; then
    echo "Error: config file not found: $config_file" >&2
    exit 1
  fi
  local entry
  entry=$(jq -c '.auths["ghcr.io"] // .auths["https://ghcr.io"] // empty' < "$config_file")
  if [[ -z "$entry" || "$entry" == "null" ]]; then
    echo "Error: no ghcr.io credentials found in $config_file" >&2
    exit 1
  fi

  local password
  password=$(jq -r '.password // empty' <<<"$entry")
  if [[ -n "$password" ]]; then
    echo "$password"
    return
  fi

  local auth
  auth=$(jq -r '.auth // empty' <<<"$entry")
  if [[ -z "$auth" ]]; then
    echo "Error: ghcr.io entry in $config_file has neither .password nor .auth" >&2
    exit 1
  fi

  local decoded
  if ! decoded=$(printf '%s' "$auth" | base64 -d 2>/dev/null); then
    echo "Error: invalid base64 in ghcr.io .auth field of $config_file" >&2
    exit 1
  fi
  if [[ "$decoded" != *:* ]]; then
    echo "Error: ghcr.io .auth in $config_file is not in 'user:token' form" >&2
    exit 1
  fi
  echo "${decoded#*:}"
}

list_ghcr_packages() {
  local org="$1" token="$2"
  local page=1
  while true; do
    local resp count
    if ! resp=$(curl -sf \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${token}" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/orgs/${org}/packages?package_type=container&per_page=100&page=${page}"); then
      echo "Error: GHCR API request failed for org '${org}' (page ${page})" >&2
      return 1
    fi
    if ! echo "$resp" | jq -e 'type == "array"' >/dev/null; then
      local msg
      msg=$(echo "$resp" | jq -r '.message // "unknown error"')
      echo "Error: GHCR API returned non-array response for org '${org}': ${msg}" >&2
      return 1
    fi
    echo "$resp" | jq -r '.[].name'
    count=$(echo "$resp" | jq 'length')
    [[ "$count" -lt 100 ]] && break
    ((page++))
  done
}

list_ghcr_tags() {
  local repo="$1" token="$2"
  local url="https://ghcr.io/v2/${repo}/tags/list"
  local resp
  local challenge
  challenge=$(curl -si "$url" 2>/dev/null | grep -i www-authenticate || true)
  if [[ "$challenge" =~ realm=\"([^\"]+)\" ]]; then
    local realm="${BASH_REMATCH[1]}"
    local scope="" service=""
    [[ "$challenge" =~ scope=\"([^\"]+)\" ]] && scope="${BASH_REMATCH[1]}"
    [[ "$challenge" =~ service=\"([^\"]+)\" ]] && service="${BASH_REMATCH[1]}"
    if [[ -z "$scope" || -z "$service" ]]; then
      echo "Error: malformed WWW-Authenticate header for ${repo} (missing scope or service)" >&2
      return 1
    fi
    local bearer
    if ! bearer=$(curl -sf -u "USERNAME:${token}" "${realm}?scope=${scope}&service=${service}" | jq -r '.token'); then
      return 1
    fi
    if ! resp=$(curl -sf -H "Authorization: Bearer ${bearer}" "$url"); then
      return 1
    fi
  else
    if ! resp=$(curl -sf -H "Authorization: Bearer ${token}" "$url"); then
      return 1
    fi
  fi
  echo "$resp" | jq -r '.tags[]?' 2>/dev/null
}

# ---- Parallel tag fetching ----

limit_jobs() {
  while (( $(jobs -r | wc -l) >= max_parallel )); do
    # wait -n (bash 4.3+) blocks until any background job exits.
    wait -n 2>/dev/null || true
  done
}

# Emit a single repo's filtered "ref:tag" lines on stdout.
# Returns non-zero if the tags list could not be fetched.
emit_dockerhub_image() {
  local namespace="$1" repo="$2"
  local tags
  if ! tags=$(list_dockerhub_tags "${namespace}/${repo}"); then
    return 1
  fi
  local tag
  while IFS= read -r tag; do
    [[ -z "$tag" ]] && continue
    tag_matches "$repo" "$tag" || continue
    echo "${namespace}/${repo}:${tag}"
  done <<<"$tags"
}

emit_ghcr_image() {
  local namespace="$1" pkg="$2" token="$3"
  local tags
  if ! tags=$(list_ghcr_tags "${namespace}/${pkg}" "$token"); then
    return 1
  fi
  local tag
  while IFS= read -r tag; do
    [[ -z "$tag" ]] && continue
    tag_matches "$pkg" "$tag" || continue
    echo "ghcr.io/${namespace}/${pkg}:${tag}"
  done <<<"$tags"
}

# ---- AIMClusterModelSource emission ----

write_modelsource() {
  local name="$1" file="$2" images_src="$3" reg="$4" secret_name="$5"
  # All user-derived string scalars are double-quoted so weird input (colons,
  # leading dashes, YAML reserved words) cannot break the generated manifest.
  {
    echo "---"
    echo "# Generated by $(basename "$0") on $(date -u '+%Y-%m-%dT%H:%M:%SZ')."
    echo "# Review and tune the fields below before applying."
    echo "apiVersion: aim.eai.amd.com/v1alpha1"
    echo "kind: AIMClusterModelSource"
    echo "metadata:"
    echo "  name: \"${name}\""
    echo "spec:"
    echo "  registry: \"${reg}\""
    echo "  # How often the operator re-syncs from the registry. Minimum recommended: 15m."
    echo "  syncInterval: 1h"
    echo "  # Maximum number of AIMClusterModel resources this source may create."
    echo "  maxModels: ${MODELSOURCE_MAX_IMAGES}"
    if [[ -n "$secret_name" ]]; then
      echo "  # Pull secret must exist in the operator namespace (typically aim-system)."
      echo "  imagePullSecrets:"
      echo "    - name: \"${secret_name}\""
    else
      echo "  # Uncomment for private registries; secret must exist in operator namespace."
      echo "  # imagePullSecrets:"
      echo "  #   - name: my-pull-secret"
    fi
    echo "  images:"
    while IFS= read -r img; do
      [[ -z "$img" ]] && continue
      echo "    - \"${img}\""
    done < "$images_src"
  } > "$file"
}

# ---- Main ----

if [[ ${#filters[@]} -eq 0 ]]; then
  filter_summary="all"
else
  filter_summary="${filters[*]}"
fi
echo "# Listing images from ${registry}/${namespace} (filters: ${filter_summary}) ..." >&2

images_file=$(mktemp)
failures_file=$(mktemp)
trap 'rm -f "$images_file" "$failures_file"' EXIT

case "$registry" in
  docker.io)
    matching_repos=()
    while IFS= read -r repo; do
      [[ -z "$repo" ]] && continue
      repo_matches "$repo" || continue
      matching_repos+=("$repo")
    done < <(list_dockerhub_repos "$namespace")

    if [[ ${#matching_repos[@]} -gt 0 ]]; then
      echo "# Fetching tags for ${#matching_repos[@]} repos ..." >&2
      for repo in "${matching_repos[@]}"; do
        (
          # Single open of the file per subshell preserves O_APPEND atomicity
          # for the per-tag echo writes that emit_dockerhub_image performs.
          if ! emit_dockerhub_image "$namespace" "$repo" >> "$images_file"; then
            echo "$repo" >> "$failures_file"
          fi
        ) &
        limit_jobs
      done
      wait
    fi
    ;;

  ghcr.io)
    token=$(extract_ghcr_token)
    matching_pkgs=()
    while IFS= read -r pkg; do
      [[ -z "$pkg" ]] && continue
      repo_matches "$pkg" || continue
      matching_pkgs+=("$pkg")
    done < <(list_ghcr_packages "$namespace" "$token")

    if [[ ${#matching_pkgs[@]} -gt 0 ]]; then
      echo "# Fetching tags for ${#matching_pkgs[@]} packages ..." >&2
      for pkg in "${matching_pkgs[@]}"; do
        (
          if ! emit_ghcr_image "$namespace" "$pkg" "$token" >> "$images_file"; then
            echo "$pkg" >> "$failures_file"
          fi
        ) &
        limit_jobs
      done
      wait
    fi
    ;;

  *)
    echo "Error: unsupported registry '$registry'. Supported: docker.io, ghcr.io" >&2
    exit 1
    ;;
esac

# Emit the discovered list on stdout regardless of any downstream errors, so
# the user still sees what was found before any limit/failure summary fires.
cat "$images_file"

count=$(wc -l < "$images_file" | tr -d ' ')
failed=$(wc -l < "$failures_file" | tr -d ' ')

if [[ -n "$create_modelsource" ]]; then
  if [[ "$count" -eq 0 ]]; then
    echo "Warning: no images discovered; skipping AIMClusterModelSource '${create_modelsource}'." >&2
  elif [[ "$count" -gt "$MODELSOURCE_MAX_IMAGES" ]]; then
    echo "Error: discovered ${count} images, which exceeds the AIMClusterModelSource cap of ${MODELSOURCE_MAX_IMAGES}." >&2
    echo "       Refusing to write AIMClusterModelSource '${create_modelsource}'." >&2
    echo "       Narrow the filter with -f (e.g. tighter tag glob), or rerun without -M." >&2
    exit 3
  else
    write_modelsource \
      "$create_modelsource" \
      "$modelsource_output" \
      "$images_file" \
      "$registry" \
      "$create_secret"
    echo "# AIMClusterModelSource '${create_modelsource}' written to ${modelsource_output}" >&2
  fi
fi

if [[ "$failed" -gt 0 ]]; then
  echo "# Found ${count} images (${failed} repos failed - see errors above)" >&2
  exit 2
fi
echo "# Found ${count} images" >&2