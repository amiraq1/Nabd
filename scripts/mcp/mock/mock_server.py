#!/usr/bin/env python3
"""Mock MCP server for internal/mcp Phase 1 client tests.

Speaks newline-delimited JSON-RPC 2.0 over stdio. Validates the client handshake
(init protocolVersion, notifications/initialized ordering) and, when asked,
acts as an adversarial peer: it sends server->client requests and reports what
the client did back through notifications plus its exit status.
"""
import argparse
import json
import sys
import threading
import time


def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def notify(method):
    send({"jsonrpc": "2.0", "method": method})


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--protocol-version", default="2024-11-05")
    ap.add_argument("--check-init-version", default="2026-07-28")
    ap.add_argument("--tools", type=int, default=2)
    ap.add_argument("--server-request", default=None,
                    help="server->client request method to send, e.g. roots/list")
    ap.add_argument("--server-ping", action="store_true")
    args = ap.parse_args()

    state = {"initialized": False}

    def handle(line):
        try:
            msg = json.loads(line)
        except Exception:
            return
        method = msg.get("method")
        mid = msg.get("id")
        params = msg.get("params") or {}
        if method == "initialize":
            if params.get("protocolVersion") != args.check_init_version:
                sys.stderr.write("bad init protocolVersion: %r\n" % (params.get("protocolVersion"),))
                sys.stderr.flush()
                sys.exit(3)
            send({"jsonrpc": "2.0", "id": mid,
                  "result": {"protocolVersion": args.protocol_version,
                             "capabilities": {"tools": {}},
                             "serverInfo": {"name": "mock", "version": "0.1"}}})
        elif method == "notifications/initialized":
            state["initialized"] = True
        elif method == "tools/list":
            if not state["initialized"]:
                sys.stderr.write("tools/list before notifications/initialized\n")
                sys.stderr.flush()
                sys.exit(4)
            tools = [{"name": "tool_%d" % i,
                      "description": "mock tool %d" % i,
                      "inputSchema": {"type": "object"}} for i in range(args.tools)]
            send({"jsonrpc": "2.0", "id": mid, "result": {"tools": tools}})
        elif method == "tools/call":
            name = params.get("name", "")
            send({"jsonrpc": "2.0", "id": mid,
                  "result": {"content": [{"type": "text", "text": "called:" + name}]}})
        elif method == "ping":
            send({"jsonrpc": "2.0", "id": mid, "result": {}})
        elif method == "notifications/cancelled":
            pass
        elif "id" in msg and ("result" in msg or "error" in msg):
            # Response to our server-initiated request: validate immediately
            # and report back through a notification the test can observe.
            if mid == 9001:
                if "error" not in msg and msg.get("result") == {}:
                    notify("test/ping-validated")
                else:
                    notify("test/ping-rejected")
            elif mid == 9002:
                err = msg.get("error") or {}
                if err.get("code") == -32601:
                    notify("test/refusal-validated")
                else:
                    notify("test/refusal-rejected")

    def harass():
        for _ in range(100):
            if state["initialized"]:
                break
            time.sleep(0.1)
        else:
            # Handshake never completed (e.g. the client refused the version).
            # Return quietly; the Go test asserts the handshake outcome itself
            # and EOF on stdin ends the process.
            return
        time.sleep(0.2)
        if args.server_ping:
            send({"jsonrpc": "2.0", "id": 9001, "method": "ping"})
        if args.server_request:
            send({"jsonrpc": "2.0", "id": 9002, "method": args.server_request,
                  "params": {}})

    threading.Thread(target=harass, daemon=True).start()
    try:
        for line in sys.stdin:
            line = line.strip()
            if line:
                handle(line)
    except BrokenPipeError:
        pass
    sys.exit(0)


main()
