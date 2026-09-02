#!/usr/bin/env python3
"""Minimal controlled-Codex protocol stub for deterministic E2E runs."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import select
import signal
import socket
import struct
import sys
import threading
from urllib.parse import urlparse


WEBSOCKET_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


def _read_exact(conn: socket.socket, size: int) -> bytes:
    chunks: list[bytes] = []
    remaining = size
    while remaining:
        chunk = conn.recv(remaining)
        if not chunk:
            raise EOFError("websocket peer closed")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def _handshake(conn: socket.socket) -> None:
    request = bytearray()
    while b"\r\n\r\n" not in request:
        chunk = conn.recv(4096)
        if not chunk:
            raise EOFError("websocket peer closed during handshake")
        request.extend(chunk)
        if len(request) > 65536:
            raise ValueError("websocket handshake headers are too large")

    headers: dict[str, str] = {}
    for line in bytes(request).split(b"\r\n")[1:]:
        if b":" not in line:
            continue
        name, value = line.split(b":", 1)
        headers[name.decode("ascii").strip().lower()] = value.decode("ascii").strip()
    key = headers.get("sec-websocket-key")
    if not key:
        raise ValueError("missing Sec-WebSocket-Key")
    accept = base64.b64encode(hashlib.sha1((key + WEBSOCKET_GUID).encode("ascii")).digest()).decode("ascii")
    conn.sendall(
        (
            "HTTP/1.1 101 Switching Protocols\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Accept: {accept}\r\n"
            "\r\n"
        ).encode("ascii")
    )


def _read_frame(conn: socket.socket) -> tuple[int, bytes]:
    first, second = _read_exact(conn, 2)
    opcode = first & 0x0F
    masked = bool(second & 0x80)
    length = second & 0x7F
    if length == 126:
        length = struct.unpack("!H", _read_exact(conn, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", _read_exact(conn, 8))[0]
    mask = _read_exact(conn, 4) if masked else b""
    payload = _read_exact(conn, length)
    if mask:
        payload = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
    return opcode, payload


def _write_frame(conn: socket.socket, opcode: int, payload: bytes, *, masked: bool = False) -> None:
    header = bytearray([0x80 | opcode])
    mask_bit = 0x80 if masked else 0
    if len(payload) < 126:
        header.append(mask_bit | len(payload))
    elif len(payload) < 65536:
        header.append(mask_bit | 126)
        header.extend(struct.pack("!H", len(payload)))
    else:
        header.append(mask_bit | 127)
        header.extend(struct.pack("!Q", len(payload)))
    if masked:
        mask = os.urandom(4)
        payload = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
        header.extend(mask)
    conn.sendall(bytes(header) + payload)


def _rpc_response(request: dict[str, object]) -> dict[str, object]:
    method = str(request.get("method", ""))
    response: dict[str, object] = {"id": request["id"]}
    if method == "initialize":
        response["result"] = {}
        return response
    if method == "thread/list":
        response["result"] = {"data": []}
        return response
    response["error"] = {
        "code": -32601,
        "message": f"method not implemented by deterministic Codex stub: {method}",
    }
    return response


def _serve_connection(conn: socket.socket) -> None:
    with conn:
        try:
            _handshake(conn)
            while True:
                opcode, payload = _read_frame(conn)
                if opcode == 0x8:
                    _write_frame(conn, 0x8, payload)
                    return
                if opcode == 0x9:
                    _write_frame(conn, 0xA, payload)
                    continue
                if opcode != 0x1:
                    continue
                request = json.loads(payload)
                if "id" not in request:
                    continue
                response = _rpc_response(request)
                _write_frame(conn, 0x1, json.dumps(response, separators=(",", ":")).encode("utf-8"))
        except (EOFError, OSError, ValueError, json.JSONDecodeError):
            return


def _endpoint(argv: list[str]) -> tuple[str, int]:
    try:
        listen_index = argv.index("--listen")
        raw = argv[listen_index + 1]
    except (ValueError, IndexError) as exc:
        raise SystemExit("codex app-server stub requires --listen ws://127.0.0.1:<port>") from exc
    parsed = urlparse(raw)
    if parsed.scheme != "ws" or parsed.hostname not in {"127.0.0.1", "localhost"} or parsed.port is None:
        raise SystemExit(f"unsupported app-server endpoint: {raw}")
    return parsed.hostname, parsed.port


def _remote_endpoint(argv: list[str]) -> str:
    try:
        remote_index = argv.index("--remote")
        raw = argv[remote_index + 1]
    except (ValueError, IndexError) as exc:
        raise ValueError("codex remote stub requires --remote ws://127.0.0.1:<port>") from exc
    parsed = urlparse(raw)
    if parsed.scheme != "ws" or parsed.hostname not in {"127.0.0.1", "localhost"} or parsed.port is None:
        raise ValueError(f"unsupported remote endpoint: {raw}")
    return raw


def _connect_remote(raw_endpoint: str) -> socket.socket:
    parsed = urlparse(raw_endpoint)
    assert parsed.hostname is not None and parsed.port is not None
    conn = socket.create_connection((parsed.hostname, parsed.port), timeout=2.0)
    key = base64.b64encode(os.urandom(16)).decode("ascii")
    target = parsed.path or "/"
    if parsed.query:
        target += f"?{parsed.query}"
    request = (
        f"GET {target} HTTP/1.1\r\n"
        f"Host: {parsed.hostname}:{parsed.port}\r\n"
        "Upgrade: websocket\r\n"
        "Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        "Sec-WebSocket-Version: 13\r\n"
        "\r\n"
    )
    conn.sendall(request.encode("ascii"))
    response = bytearray()
    while b"\r\n\r\n" not in response:
        chunk = conn.recv(4096)
        if not chunk:
            raise EOFError("app-server closed during remote websocket handshake")
        response.extend(chunk)
        if len(response) > 65536:
            raise ValueError("remote websocket handshake headers are too large")
    head = bytes(response).split(b"\r\n\r\n", 1)[0]
    lines = head.split(b"\r\n")
    if not lines or b" 101 " not in lines[0]:
        raise ValueError(f"remote websocket handshake was rejected: {lines[0] if lines else head!r}")
    headers: dict[str, str] = {}
    for line in lines[1:]:
        if b":" not in line:
            continue
        name, value = line.split(b":", 1)
        headers[name.decode("ascii").strip().lower()] = value.decode("ascii").strip()
    expected = base64.b64encode(hashlib.sha1((key + WEBSOCKET_GUID).encode("ascii")).digest()).decode("ascii")
    if headers.get("sec-websocket-accept") != expected:
        raise ValueError("remote websocket handshake returned the wrong Sec-WebSocket-Accept")
    conn.settimeout(None)
    return conn


def _initialize_remote(conn: socket.socket) -> None:
    request = {
        "id": 1,
        "method": "initialize",
        "params": {"clientInfo": {"name": "aft-codex-stub", "title": "AFT Codex stub", "version": "1"}},
    }
    _write_frame(conn, 0x1, json.dumps(request, separators=(",", ":")).encode("utf-8"), masked=True)
    while True:
        opcode, payload = _read_frame(conn)
        if opcode == 0x9:
            _write_frame(conn, 0xA, payload, masked=True)
            continue
        if opcode != 0x1:
            raise ValueError(f"unexpected websocket opcode during initialize: {opcode}")
        response = json.loads(payload)
        if response.get("id") != 1:
            continue
        if "error" in response or "result" not in response:
            raise ValueError(f"app-server rejected initialize: {response}")
        break
    notification = {"method": "initialized", "params": {}}
    _write_frame(conn, 0x1, json.dumps(notification, separators=(",", ":")).encode("utf-8"), masked=True)


def _run_remote(argv: list[str]) -> int:
    endpoint = _remote_endpoint(argv)
    prompt = argv[-1] if argv else ""
    safety_header = "### Multi-Agent Safety Rules"
    # GenerateTerminalPromptText adds two newlines before a guardrail block that
    # itself begins with one newline. Split at that exact boundary so the
    # fingerprint covers only the operator-authored bytes.
    safety_separator = f"\n\n\n{safety_header}"
    prompt_prefix = prompt.rsplit(safety_separator, 1)[0] if safety_separator in prompt else prompt
    prompt_fingerprint = hashlib.sha256(prompt_prefix.encode("utf-8")).hexdigest()
    safety_blocks = prompt.count(safety_header)
    with _connect_remote(endpoint) as conn:
        _initialize_remote(conn)
        print("Codex")
        print()
        print(os.environ.get("STUB_CODEX_RESPONSE", "I completed the requested task successfully."))
        print()
        print(f"Controlled Codex stub connected: {os.environ.get('LOOM_AGENT_NAME', 'unknown')}", flush=True)
        print(
            f"Controlled Codex prompt contract: prefix-sha256={prompt_fingerprint} safety-blocks={safety_blocks}",
            flush=True,
        )
        stdin_fd = sys.stdin.fileno()
        while True:
            readable, _, _ = select.select([stdin_fd, conn], [], [])
            if conn in readable:
                opcode, payload = _read_frame(conn)
                if opcode == 0x8:
                    return 1
                if opcode == 0x9:
                    _write_frame(conn, 0xA, payload, masked=True)
            if stdin_fd in readable:
                if os.read(stdin_fd, 4096):
                    continue
                try:
                    _write_frame(conn, 0x8, b"", masked=True)
                except OSError:
                    pass
                return 0


def main(argv: list[str]) -> int:
    if argv and argv[0] == "--remote-client":
        try:
            return _run_remote(argv[1:])
        except (EOFError, OSError, ValueError, json.JSONDecodeError) as exc:
            print(f"codex remote stub failed to connect: {exc}", file=sys.stderr)
            return 1

    host, port = _endpoint(argv)
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind((host, port))
    server.listen()

    def stop(_signum: int, _frame: object) -> None:
        server.close()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    while True:
        try:
            conn, _addr = server.accept()
        except OSError:
            return 0
        threading.Thread(target=_serve_connection, args=(conn,), daemon=True).start()


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
