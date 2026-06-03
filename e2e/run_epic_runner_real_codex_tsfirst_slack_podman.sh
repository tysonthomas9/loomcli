#!/usr/bin/env bash
# Build and run the TypeScript-SDK-driven real Codex epic-runner E2E in Podman,
# seeded with the Slack-clone app fixture. Mounts scripts/fixtures/slack-src into
# the container and runs epic_runner_real_codex_tsfirst_slack.sh.

set -euo pipefail

IMAGE="${IMAGE:-loomcli-epic-runner-e2e}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# fleet-db may sit as a sibling of loomcli (this checkout) or one level up.
if [[ -z "${FLEET_DB_REPO:-}" ]]; then
    if [[ -d "$ROOT_DIR/../fleet-db/cmd/fleet-db" ]]; then
        FLEET_DB_REPO="$ROOT_DIR/../fleet-db"
    else
        FLEET_DB_REPO="$ROOT_DIR/../../fleet-db"
    fi
fi
FLEET_DB_BIN="${FLEET_DB_BIN:-}"
CODEX_HOME="${CODEX_HOME:-/private/tmp/codex-e2e-home}"
SLACK_SRC_DIR="${SLACK_SRC_DIR:-$ROOT_DIR/scripts/fixtures/slack-src}"
ARTIFACTS_DIR="${ARTIFACTS_DIR:-/private/tmp/loom-tsfirst-slack-codex-epic-artifacts}"
EPIC_RUNNER_TIMEOUT="${EPIC_RUNNER_TIMEOUT:-900s}"
RECONCILE_INTERVAL="${RECONCILE_INTERVAL:-3}"
CODEX_VERSION="${CODEX_VERSION:-0.129.0}"
AGENT_RUNTIME="${AGENT_RUNTIME:-local}"
DAYTONA_REMOTE_REPO_URL="${DAYTONA_REMOTE_REPO_URL:-}"
DAYTONA_FORCE_PUSH_REMOTE="${DAYTONA_FORCE_PUSH_REMOTE:-}"
DAYTONA_SNAPSHOT="${DAYTONA_SNAPSHOT:-}"
DAYTONA_TARGET="${DAYTONA_TARGET:-}"
DAYTONA_GIT_USERNAME="${DAYTONA_GIT_USERNAME:-x-access-token}"
DAYTONA_GIT_TOKEN_ENV="${DAYTONA_GIT_TOKEN_ENV:-}"

is_shell_identifier() {
    [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]
}

env_value() {
    local name="$1"
    if [[ -n "$name" ]] && is_shell_identifier "$name"; then
        printf '%s' "${!name:-}"
    fi
}

daytona_push_token_available() {
    [[ -n "$(env_value "$DAYTONA_GIT_TOKEN_ENV")" || -n "${GITHUB_TOKEN:-}" || -n "${GH_TOKEN:-}" ]]
}

validate_daytona_codex_auth_file() {
    local auth_file="${CODEX_AUTH_FILE:-}"
    [[ -n "$auth_file" ]] || return 0
    if [[ "$auth_file" != /* ]]; then
        echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must be an absolute Daytona remote path, got: $auth_file" >&2
        exit 1
    fi
    case "$auth_file" in
        /Users/*|/private/*|/Volumes/*|/var/folders/*)
            echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must point at a Daytona-provisioned remote auth.json, not host-local path: $auth_file" >&2
            exit 1
            ;;
    esac
    if [[ "${auth_file##*/}" != "auth.json" ]]; then
        echo "AGENT_RUNTIME=daytona CODEX_AUTH_FILE must point to auth.json, got: $auth_file" >&2
        exit 1
    fi
}

validate_daytona_remote_repo_url() {
    local repo_url="$DAYTONA_REMOTE_REPO_URL"
    if [[ -z "$repo_url" ]]; then
        echo "AGENT_RUNTIME=daytona requires DAYTONA_REMOTE_REPO_URL for a Git remote reachable from Daytona" >&2
        exit 1
    fi
    if [[ "$repo_url" == *"://"* ]]; then
        local scheme="${repo_url%%://*}"
        case "$scheme" in
            http|https|ssh|git|file) ;;
            *)
                echo "AGENT_RUNTIME=daytona DAYTONA_REMOTE_REPO_URL uses unsupported scheme '$scheme': $repo_url" >&2
                exit 1
                ;;
        esac
    fi
    case "$repo_url" in
        http:*|https:*|ssh:*|git:*|file:*|ftp:*)
            if [[ "$repo_url" != *"://"* ]]; then
                local scheme="${repo_url%%:*}"
                echo "AGENT_RUNTIME=daytona DAYTONA_REMOTE_REPO_URL uses malformed scheme '$scheme'; use $scheme://... or scp-like Git syntax: $repo_url" >&2
                exit 1
            fi
            ;;
    esac
    case "$repo_url" in
        /*|./*|../*|~/*|file://*)
            echo "AGENT_RUNTIME=daytona cannot use a host-local DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
        http://localhost*|https://localhost*|ssh://*localhost*|git://localhost*|*localhost:*|*host.docker.internal*)
            echo "AGENT_RUNTIME=daytona cannot use a localhost DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
        http://127.*|https://127.*|ssh://*127.*|git://127.*|*@127.*:*|127.*:*|http://0.0.0.0*|https://0.0.0.0*|ssh://*0.0.0.0*|*@0.0.0.0:*)
            echo "AGENT_RUNTIME=daytona cannot use a loopback DAYTONA_REMOTE_REPO_URL: $repo_url" >&2
            exit 1
            ;;
    esac
}

validate_daytona_wrapper_env() {
    if [[ -z "${DAYTONA_API_KEY:-}" ]]; then
        echo "AGENT_RUNTIME=daytona requires DAYTONA_API_KEY" >&2
        exit 1
    fi
    if [[ -z "${OPENAI_API_KEY:-}" && -z "${CODEX_AUTH_FILE:-}" ]]; then
        echo "AGENT_RUNTIME=daytona requires OPENAI_API_KEY or a Daytona-provisioned CODEX_AUTH_FILE" >&2
        exit 1
    fi
    validate_daytona_codex_auth_file
    if [[ -z "${LOOM_FLEET_DB_URL:-}" ]]; then
        echo "AGENT_RUNTIME=daytona requires LOOM_FLEET_DB_URL pointing at a URL reachable from Daytona" >&2
        exit 1
    fi
    if [[ "$LOOM_FLEET_DB_URL" != *"://"* ]]; then
        echo "AGENT_RUNTIME=daytona LOOM_FLEET_DB_URL must use http or https: $LOOM_FLEET_DB_URL" >&2
        exit 1
    fi
    local fleet_scheme="${LOOM_FLEET_DB_URL%%://*}"
    case "$fleet_scheme" in
        http|https) ;;
        *)
            echo "AGENT_RUNTIME=daytona LOOM_FLEET_DB_URL must use http or https, got scheme '$fleet_scheme': $LOOM_FLEET_DB_URL" >&2
            exit 1
            ;;
    esac
    case "$LOOM_FLEET_DB_URL" in
        /*|http://localhost*|https://localhost*|http://127.*|https://127.*|http://0.0.0.0*|https://0.0.0.0*|http://host.docker.internal*|https://host.docker.internal*|http://10.*|https://10.*|http://192.168.*|https://192.168.*|http://169.254.*|https://169.254.*|http://172.1[6-9].*|https://172.1[6-9].*|http://172.2[0-9].*|https://172.2[0-9].*|http://172.3[0-1].*|https://172.3[0-1].*)
            echo "AGENT_RUNTIME=daytona cannot use a host-local LOOM_FLEET_DB_URL: $LOOM_FLEET_DB_URL" >&2
            exit 1
            ;;
    esac
    if [[ -z "$DAYTONA_REMOTE_REPO_URL" ]]; then
        echo "AGENT_RUNTIME=daytona requires DAYTONA_REMOTE_REPO_URL for a Git remote reachable from Daytona" >&2
        exit 1
    fi
    validate_daytona_remote_repo_url
    case "$DAYTONA_REMOTE_REPO_URL" in
        http://*|https://*)
            if ! daytona_push_token_available; then
                if [[ "$DAYTONA_GIT_TOKEN_ENV" == "GITHUB_TOKEN" || -z "$DAYTONA_GIT_TOKEN_ENV" ]]; then
                    echo "AGENT_RUNTIME=daytona requires GITHUB_TOKEN or GH_TOKEN to seed HTTPS remote $DAYTONA_REMOTE_REPO_URL" >&2
                else
                    echo "AGENT_RUNTIME=daytona requires $DAYTONA_GIT_TOKEN_ENV, GITHUB_TOKEN, or GH_TOKEN to seed HTTPS remote $DAYTONA_REMOTE_REPO_URL" >&2
                fi
                exit 1
            fi
            ;;
    esac
}

if [[ "$AGENT_RUNTIME" != "local" && "$AGENT_RUNTIME" != "daytona" ]]; then
    echo "AGENT_RUNTIME must be local or daytona, got: $AGENT_RUNTIME" >&2
    exit 1
fi
if [[ "$AGENT_RUNTIME" == "daytona" ]]; then
    if [[ -n "$DAYTONA_GIT_TOKEN_ENV" ]] && ! is_shell_identifier "$DAYTONA_GIT_TOKEN_ENV"; then
        echo "DAYTONA_GIT_TOKEN_ENV must be a valid environment variable name, got: $DAYTONA_GIT_TOKEN_ENV" >&2
        exit 1
    fi
    validate_daytona_wrapper_env
fi

if [[ "$AGENT_RUNTIME" == "local" && ! -d "$CODEX_HOME" ]]; then
    echo "Codex home not found at $CODEX_HOME" >&2
    echo "Set CODEX_HOME to a directory containing real Codex CLI auth/config." >&2
    exit 1
fi

if [[ ! -d "$SLACK_SRC_DIR" ]]; then
    echo "slack-src fixture not found at $SLACK_SRC_DIR" >&2
    exit 1
fi

podman build -f "$ROOT_DIR/e2e/Dockerfile" -t "$IMAGE" "$ROOT_DIR"

IMAGE_ARCH="$(podman image inspect "$IMAGE" --format '{{.Architecture}}')"
if [[ -z "$IMAGE_ARCH" ]]; then
    echo "could not determine image architecture for $IMAGE" >&2
    exit 1
fi
if [[ -z "$FLEET_DB_BIN" ]]; then
    FLEET_DB_BIN="$ROOT_DIR/tmp/e2e/fleet-db-linux-$IMAGE_ARCH"
fi

# Build fleet-db for the container's OS/arch if missing or not a Linux ELF
# (a stale macOS/host build mounted into the container fails with "exec format error").
if [[ ! -x "$FLEET_DB_BIN" ]] || ! file -b "$FLEET_DB_BIN" 2>/dev/null | grep -q 'ELF'; then
    if [[ ! -d "$FLEET_DB_REPO/cmd/fleet-db" ]]; then
        echo "fleet-db repo not found at $FLEET_DB_REPO" >&2
        echo "Set FLEET_DB_REPO or FLEET_DB_BIN before running this E2E." >&2
        exit 1
    fi
    mkdir -p "$(dirname "$FLEET_DB_BIN")"
    (cd "$FLEET_DB_REPO" && GOOS=linux GOARCH="$IMAGE_ARCH" CGO_ENABLED=0 go build -o "$FLEET_DB_BIN" ./cmd/fleet-db)
fi

if ! file -b "$FLEET_DB_BIN" | grep -q 'ELF'; then
    echo "fleet-db binary is not a Linux ELF: $(file -b "$FLEET_DB_BIN")" >&2
    echo "path: $FLEET_DB_BIN" >&2
    exit 1
fi

mkdir -p "$ARTIFACTS_DIR"

PODMAN_ARGS=(
    --rm
    -e "EPIC_RUNNER_TIMEOUT=$EPIC_RUNNER_TIMEOUT"
    -e "RECONCILE_INTERVAL=$RECONCILE_INTERVAL"
    -e "CODEX_VERSION=$CODEX_VERSION"
    -e "AGENT_RUNTIME=$AGENT_RUNTIME"
    -e "DAYTONA_REMOTE_REPO_URL=$DAYTONA_REMOTE_REPO_URL"
    -e "DAYTONA_FORCE_PUSH_REMOTE=$DAYTONA_FORCE_PUSH_REMOTE"
    -e "DAYTONA_SNAPSHOT=$DAYTONA_SNAPSHOT"
    -e "DAYTONA_TARGET=$DAYTONA_TARGET"
    -e "DAYTONA_GIT_USERNAME=$DAYTONA_GIT_USERNAME"
    -e "DAYTONA_GIT_TOKEN_ENV=$DAYTONA_GIT_TOKEN_ENV"
    -e "SLACK_SRC_DIR=/opt/slack-src"
    -e "ARTIFACTS_OUT=/artifacts"
    -v "$FLEET_DB_BIN:/usr/local/bin/fleet-db:ro"
    -v "$ROOT_DIR/e2e/epic_runner_real_codex_tsfirst_slack.sh:/usr/local/bin/epic_runner_real_codex_tsfirst_slack.sh:ro"
    -v "$SLACK_SRC_DIR:/opt/slack-src:ro"
    -v "$ARTIFACTS_DIR:/artifacts"
)

if [[ "$AGENT_RUNTIME" == "local" ]]; then
    PODMAN_ARGS+=(-v "$CODEX_HOME:/root/.codex")
else
    PODMAN_ARGS+=(
        --env DAYTONA_API_KEY
        --env OPENAI_API_KEY
        --env CODEX_AUTH_FILE
        --env GITHUB_TOKEN
        --env GH_TOKEN
        --env LOOM_FLEET_DB_URL
        --env LOOM_FLEET_DB_API_KEY
        --env LOOM_FLEET_DB_ACTOR
    )
    if [[ -n "$DAYTONA_GIT_TOKEN_ENV" ]]; then
        PODMAN_ARGS+=(--env "$DAYTONA_GIT_TOKEN_ENV")
    fi
fi

podman run "${PODMAN_ARGS[@]}" "$IMAGE" /usr/local/bin/epic_runner_real_codex_tsfirst_slack.sh
