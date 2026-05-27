#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

if [ -z "${LOOM_BIN:-}" ]; then
	mkdir -p ./bin
	go build -o ./bin/loom ./cmd/loom
	LOOM_BIN=./bin/loom
fi

json_string_field() {
	field=$1
	if command -v jq >/dev/null 2>&1; then
		jq -r ".$field // empty" 2>/dev/null || true
		return
	fi
	sed -n 's/.*"'"$field"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p'
}

tmp=$(mktemp -d)
runtime_pid=
cleanup() {
	if [ -n "$runtime_pid" ]; then
		kill "$runtime_pid" 2>/dev/null || true
	fi
	"$LOOM_BIN" local --data-dir "$tmp" stop >/dev/null 2>&1 || true
	rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

nohup "$LOOM_BIN" local --data-dir "$tmp" start >"$tmp/local-start.log" 2>&1 &
runtime_pid=$!

runtime_json="$tmp/runtime.json"
i=0
while [ "$i" -lt 100 ]; do
	if [ -f "$runtime_json" ]; then
		status=$(json_string_field status <"$runtime_json")
		if [ "$status" = "running" ]; then
			break
		fi
	fi
	i=$((i + 1))
	sleep 0.2
done

if [ ! -f "$runtime_json" ]; then
	echo "[repro] runtime.json was not written"
	exit 1
fi

url=$(json_string_field url <"$runtime_json")
if [ -z "$url" ]; then
	echo "[repro] runtime URL missing from runtime.json"
	exit 1
fi

health_code=$(curl -s -o /dev/null -w '%{http_code}' "$url/api/health")
if [ "$health_code" != "200" ]; then
	echo "[repro] expected /api/health liveness 200, got $health_code"
	exit 1
fi

probe=$(curl -s -w '\n%{http_code}' "$url/api/workspaces/NOPE/runtime-ready")
body=$(printf '%s\n' "$probe" | sed '$d')
code=$(printf '%s\n' "$probe" | tail -n 1)

if [ "$code" = "404" ]; then
	echo "[repro] expected /runtime-ready route; got 404 (route missing)"
	exit 1
fi

if [ "$code" != "503" ]; then
	echo "[repro] expected /runtime-ready 503, got $code"
	printf '%s\n' "$body"
	exit 1
fi

if command -v jq >/dev/null 2>&1; then
	ready=$(printf '%s\n' "$body" | jq -r '.ready')
	reason=$(printf '%s\n' "$body" | jq -r '.reason // empty')
	if [ "$ready" != "false" ] || [ -z "$reason" ]; then
		echo "[repro] expected ready=false with diagnostic reason"
		printf '%s\n' "$body"
		exit 1
	fi
	mode=$(printf '%s\n' "$body" | jq -r '.mode')
	case "$mode" in
		daemon | fleet) ;;
		*)
			echo "[repro] expected mode=daemon|fleet, got '$mode'"
			printf '%s\n' "$body"
			exit 1
			;;
	esac
else
	if ! printf '%s\n' "$body" | grep -q '"ready":false'; then
		echo "[repro] expected ready=false"
		printf '%s\n' "$body"
		exit 1
	fi
	reason=$(printf '%s\n' "$body" | json_string_field reason)
	if [ -z "$reason" ]; then
		echo "[repro] expected diagnostic reason"
		printf '%s\n' "$body"
		exit 1
	fi
	mode=$(printf '%s\n' "$body" | json_string_field mode)
	case "$mode" in
		daemon | fleet) ;;
		*)
			echo "[repro] expected mode=daemon|fleet, got '$mode'"
			printf '%s\n' "$body"
			exit 1
			;;
	esac
fi

echo "[repro] OK - /runtime-ready route returns 503 with diagnostic reason"
