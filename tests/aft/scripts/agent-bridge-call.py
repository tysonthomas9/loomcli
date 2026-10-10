#!/usr/bin/env python3
"""POST to an Agent API route as an agent, with the bridge token its harness gets.

Usage: agent-bridge-call.py <workspace> <agent_id> <route> <json body>

The token is minted as serve mints it (internal/webui/handlers/agentsv1
caller.go): "loomb1." + b64url("agent\\0<ws>\\0<id>") + "." + b64url(HMAC-SHA256),
keyed by agent-token.key in the e2e stack's Loom dir ($AFT_LOOM_CONFIG_DIR).
Prints {"status": <http status>, "body": <JSON answer>}; never prints the token.
"""
import base64, hashlib, hmac, json, os, sys, urllib.error, urllib.request

def b64(b):
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()

ws, agent, route, body = sys.argv[1:5]
key = open(os.path.join(os.environ["AFT_LOOM_CONFIG_DIR"], "agent-token.key"), "rb").read()
claims = b64(f"agent\0{ws}\0{agent}".encode())
token = "loomb1." + claims + "." + b64(hmac.new(key, claims.encode(), hashlib.sha256).digest())
req = urllib.request.Request(
    f"{os.environ['AFT_BASE_URL']}/api/workspaces/{ws}/v1/{route}", data=body.encode(), method="POST",
    headers={"Content-Type": "application/json", "Authorization": "Bearer " + token})
try:
    with urllib.request.urlopen(req, timeout=60) as r:
        status, raw = r.status, r.read()
except urllib.error.HTTPError as e:
    status, raw = e.code, e.read()
print(json.dumps({"status": status, "body": json.loads(raw or b"null")}))
