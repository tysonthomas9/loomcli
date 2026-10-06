#!/usr/bin/env python3
"""One bounded owned catalog request, with fixed-category failure evidence."""

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path
from urllib.parse import urlsplit


PATH = "/api/workspaces/LOCALMODE/v1/harnesses/opencode/models"
ALLOWED_CODES = {"harness_unavailable", "harness_error"}


def category(status, code, message):
    if status == 401 or status == 403 or "opencode: auth_failed" in message:
        return "authentication_refused"
    if "restart backoff after" in message:
        return "supervisor_backoff"
    if "no OpenCode service answered within" in message:
        return "service_start_timeout"
    if "serve --service exited during start" in message:
        return "service_exited"
    if "opencode: harness_down" in message:
        return "native_service_error"
    if code == "harness_unavailable":
        return "harness_unavailable_other"
    if code == "harness_error":
        return "harness_error_other"
    return "http_error_other"


def main(url, receipt_path):
    parsed = urlsplit(url)
    if (parsed.scheme != "http" or parsed.hostname != "127.0.0.1" or
            not parsed.port or parsed.path != PATH or parsed.query or parsed.fragment or
            parsed.username or parsed.password):
        raise ValueError("catalog URL must be the owned loopback endpoint")
    receipt = Path(receipt_path)
    if receipt.parent.is_symlink() or not receipt.parent.is_dir() or receipt.exists():
        raise ValueError("catalog receipt must be a new file in an existing directory")

    result = {"endpoint": "owned-opencode-model-catalog", "attempts": 1,
              "http_status": None, "api_code": "none", "category": "request_failed"}
    fd, body_path = tempfile.mkstemp(prefix=".model-catalog-", dir=receipt.parent)
    os.close(fd)
    body = b""
    try:
        try:
            response = subprocess.run(
                ["curl", "--silent", "--show-error", "--max-time", "30",
                 "--output", body_path, "--write-out", "%{http_code}", url],
                capture_output=True, timeout=32, check=False)
            if response.returncode:
                result["category"] = "request_timeout" if response.returncode == 28 else "request_failed"
            elif response.stdout.isdigit() and len(response.stdout) == 3:
                status = int(response.stdout)
                result["http_status"] = status
                if status == 200:
                    body = Path(body_path).read_bytes()
                    result["category"] = "ready"
                else:
                    try:
                        error = json.loads(Path(body_path).read_bytes()[:8192])
                    except (ValueError, UnicodeError):
                        error = {}
                    code = error.get("code") if isinstance(error, dict) else None
                    message = error.get("error") if isinstance(error, dict) else None
                    result["api_code"] = code if isinstance(code, str) and code in ALLOWED_CODES else "unrecognized"
                    result["category"] = category(status, result["api_code"], message if isinstance(message, str) else "")
            else:
                result["category"] = "invalid_http_status"
        except subprocess.TimeoutExpired:
            result["category"] = "request_timeout"
    finally:
        os.unlink(body_path)

    fd = os.open(receipt, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(result, stream, sort_keys=True)
        stream.write("\n")
    if result["category"] != "ready":
        print("[aft-agent-flows] model catalog readiness: " + result["category"], file=sys.stderr)
        return 1
    sys.stdout.buffer.write(body)
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: agent-flows-catalog-readiness.py URL RECEIPT")
    try:
        sys.exit(main(sys.argv[1], sys.argv[2]))
    except ValueError as exc:
        sys.exit("[aft-agent-flows] " + str(exc))
