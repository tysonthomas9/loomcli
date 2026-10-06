#!/usr/bin/env python3
"""Check the runner's disposable FleetDB overlay without starting a stack."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


ROOT = Path(__file__).resolve().parents[3]
RUNNER = ROOT / "tests/aft/run-aft-agent-flows.sh"
COMPOSE_FILES = [
    ROOT / "test/local-mode/docker-compose.yml",
    ROOT / "test/local-mode/docker-compose.agents.yml",
    ROOT / "test/local-mode/docker-compose.agents-real.yml",
]


def compose_config(extra, env):
    command = ["docker-compose", "-p", "aft-fixture-offline"]
    for file in [*COMPOSE_FILES, *extra]:
        command.extend(("-f", str(file)))
    command.extend(("config", "--format", "json"))
    return json.loads(subprocess.check_output(command, cwd=ROOT, env=env, text=True))


def manifest(runner, overlay):
    block = runner.split('jq -n --arg head', 1)[1].split('> "$run_root/evidence/manifest.json"', 1)[0]
    match = re.search(r"\n  '(\{source_head:.*\})' \\\n", block)
    assert match, "manifest filter is missing"
    command = ["jq", "-n", "--arg", "head", "fixture"]
    for is_json, name in re.findall(r"--arg(json)?\s+([A-Za-z][A-Za-z0-9_]*)\s+", block):
        if is_json:
            value = '{"batch":"git-files"}' if name == "selection" else "3"
            command.extend(("--argjson", name, value))
        else:
            value = hashlib.sha256(overlay.read_bytes()).hexdigest() if name == "fleetOverrideSha" else "fixture"
            if name == "project":
                value = "aft-fixture-offline"
            command.extend(("--arg", name, value))
    command.append(match.group(1))
    return json.loads(subprocess.check_output(command, text=True))


def main():
    runner = RUNNER.read_text()
    match = re.search(
        r'cat > "\$run_root/evidence/fleet-override.yml" <<YAML\n(.*?)\nYAML',
        runner,
        re.S,
    )
    assert match, "run-owned FleetDB override is missing"

    env = dict(
        os.environ,
        LOCAL_MODE_OPENCODE_COPY="/private/tmp/aft-fixture-offline/opencode.db",
        LOCAL_MODE_CODEX_AUTH="/dev/null",
        LOCAL_MODE_CLAUDE_AUTH="/dev/null",
        LOCAL_MODE_CLAUDE_TOKEN_FILE="/dev/null",
    )
    with tempfile.TemporaryDirectory(prefix="aft-fleet-config-", dir="/private/tmp") as temp:
        overlay = Path(temp) / "fleet-override.yml"
        overlay.write_text(match.group(1).replace("$fleet_repo", "/private/tmp/fdb1-fleet") + "\n")
        base = compose_config([], env)["services"]
        actual = compose_config([overlay], env)["services"]
        receipt = manifest(runner, overlay)
        override_sha = hashlib.sha256(overlay.read_bytes()).hexdigest()

    assert set(actual) == set(base), "override changed the service set"
    for service in base:
        if service != "fleet-db":
            assert actual[service] == base[service], f"override changed {service}"
            assert "FLEET_RATE_LIMIT_ENABLED" not in actual[service].get("environment", {})

    before = base["fleet-db"]
    after = actual["fleet-db"]
    assert after["environment"].get("FLEET_RATE_LIMIT_ENABLED") == "false"
    assert "FLEET_RATE_LIMIT_ENABLED" not in before["environment"]
    assert after["environment"] == before["environment"] | {"FLEET_RATE_LIMIT_ENABLED": "false"}
    assert after["build"]["context"] == "/private/tmp/fdb1-fleet"
    for key in set(before) | set(after):
        if key not in {"build", "environment"}:
            assert before.get(key) == after.get(key), f"FleetDB {key} changed"
    for key in set(before["build"]) | set(after["build"]):
        if key != "context":
            assert before["build"].get(key) == after["build"].get(key)
    assert receipt["owned"]["compose_project"] == "aft-fixture-offline"
    assert receipt["owned"]["fleetdb_rate_limit_enabled"] is False
    assert receipt["owned"]["fleetdb_compose_override_sha256"] == override_sha
    print("Agent AFT FleetDB overlay: owned service only, defaults preserved, manifest declared")


if __name__ == "__main__":
    main()
