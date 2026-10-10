#!/usr/bin/env python3
"""Offline, one-request catalog readiness and safe receipt checks."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


HELPER = Path(__file__).with_name("agent-flows-catalog-readiness.py")
URL = "http://127.0.0.1:54321/api/workspaces/LOCALMODE/v1/harnesses/opencode/models"


class CatalogReadinessTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.fake = self.bin / "curl"
        self.fake.write_text("""#!/usr/bin/env python3
import os, pathlib, sys
args = sys.argv[1:]
pathlib.Path(os.environ['CALLS']).write_text('called')
pathlib.Path(args[args.index('--output') + 1]).write_text(os.environ['BODY'])
sys.stdout.write(os.environ['STATUS'])
sys.exit(int(os.environ.get('EXIT', '0')))
""")
        self.fake.chmod(0o700)
        self.receipt = self.root / "receipt.json"
        self.calls = self.root / "calls"

    def invoke(self, status="200", body='{"providers":[]}', exit_code="0", url=URL):
        env = {**os.environ, "PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
               "STATUS": status, "BODY": body, "EXIT": exit_code, "CALLS": str(self.calls)}
        return subprocess.run([sys.executable, str(HELPER), url, str(self.receipt)],
                              capture_output=True, text=True, env=env, check=False)

    def test_success_returns_catalog_and_one_ready_receipt(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0)
        self.assertEqual(json.loads(result.stdout), {"providers": []})
        self.assertEqual(json.loads(self.receipt.read_text()), {
            "endpoint": "owned-opencode-model-catalog", "attempts": 1,
            "http_status": 200, "api_code": "none", "category": "ready"})
        self.assertTrue(self.calls.exists())

    def test_auth_503_is_not_retried_or_disclosed(self):
        result = self.invoke("503", '{"code":"harness_unavailable","error":"opencode: auth_failed (401 SecretTag): private-token"}')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertNotIn("private-token", result.stderr + self.receipt.read_text())
        self.assertEqual(json.loads(self.receipt.read_text())["category"], "authentication_refused")
        self.assertEqual(json.loads(self.receipt.read_text())["attempts"], 1)

    def test_startup_and_unknown_503_are_distinct_fixed_categories(self):
        result = self.invoke("503", '{"code":"harness_unavailable","error":"opencode restart backoff after 2 failures: secret"}')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(self.receipt.read_text())["category"], "supervisor_backoff")
        self.receipt.unlink()
        result = self.invoke("503", '{"code":"unexpected-secret","error":"other secret"}')
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(self.receipt.read_text())["api_code"], "unrecognized")
        self.assertNotIn("secret", self.receipt.read_text() + result.stderr)

    def test_curl_timeout_is_bounded_and_safe(self):
        result = self.invoke("000", "secret", "28")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(json.loads(self.receipt.read_text())["category"], "request_timeout")
        self.assertNotIn("secret", self.receipt.read_text() + result.stderr)

    def test_foreign_url_is_refused_before_request(self):
        result = self.invoke(url="http://example.com/api/workspaces/LOCALMODE/v1/harnesses/opencode/models")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.calls.exists())
        self.assertFalse(self.receipt.exists())


if __name__ == "__main__":
    unittest.main()
