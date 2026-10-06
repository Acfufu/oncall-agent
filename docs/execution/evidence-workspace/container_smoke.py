"""Opt-in clean Go container startup; owned temporary layer, no user volume/config."""
import json
import pathlib
import subprocess
import socket
import time
import urllib.error
import urllib.request
import uuid

out = pathlib.Path(__file__).parent
owner = "oncall-smoke-" + uuid.uuid4().hex
image = "test_oncall_workspace:20261006"
manifest = {"owner": owner, "image": image, "checks": []}
cid = None


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def request(path, token=None, data=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + path, headers=headers, data=data)
    try:
        response = urllib.request.urlopen(req, timeout=5)
    except urllib.error.HTTPError as error:
        response = error
    return response.status, response.read().decode()


try:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        host_port = sock.getsockname()[1]
    cid = command("docker", "run", "-d", "--name", owner, "--label", "oncall.smoke.owner=" + owner,
                  "-p", "127.0.0.1:" + str(host_port) + ":8819",
                  "-e", "ONCALL_SERVER_HOST=0.0.0.0", "-e", "ONCALL_LOCALHOST_HTTP=true",
                  "-e", "ONCALL_REDIS_ADDR=127.0.0.1:19993",
                  "-e", "ONCALL_CONSOLE_TOKEN=isolated-smoke-console", "-e", "ONCALL_WEBHOOK_TOKEN=isolated-smoke-webhook", image)
    manifest.update({"container_id": cid, "image_id": command("docker", "inspect", "--format", "{{.Image}}", cid)})
    port = command("docker", "port", cid, "8819/tcp").split(":")[-1]
    base = "http://127.0.0.1:" + port
    manifest["host_binding"] = base
    for _ in range(60):
        try:
            if request("/ping")[0] == 200:
                break
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(.5)
    else:
        raise RuntimeError("clean container did not become live")
    checks = [
        ("/", None, None, 200), ("/workspace/metrics", None, None, 200), ("/metrics", None, None, 401), ("/legacy", None, None, 200), ("/ready", None, None, 503),
        ("/api/v1/documents", None, None, 401), ("/mcp", None, None, 401),
        ("/api/v1/documents", "isolated-smoke-console", None, 200),
        ("/api/v1/system/status", "isolated-smoke-console", None, 200),
        ("/upload", "isolated-smoke-console", b'{"title":"SmokeCPU","content":"# SmokeCPU\\nIsolated source failure check"}', 503),
    ]
    for path, token, data, expected in checks:
        status, body = request(path, token, data)
        assert status == expected, (path, status, expected, body[:500])
        manifest["checks"].append({"path": path, "method": "POST" if data else "GET", "status": status,
                                   "body": body if len(body) < 12000 and path != "/legacy" else "HTML served"})
    assert 'source_unavailable' in manifest["checks"][-1]["body"]
    status, body = request("/metrics", "isolated-smoke-console")
    assert status == 200 and "# HELP" in body and "<html" not in body
    assert "<html" in next(c["body"] for c in manifest["checks"] if c["path"] == "/workspace/metrics")
    manifest["metrics_route"] = "PASS: /workspace/metrics SPA shell, /metrics authenticated Prometheus text"
    login_request = urllib.request.Request(base + "/api/v1/auth/session", data=b"{}",
                                          headers={"Authorization": "Bearer isolated-smoke-console", "Content-Type": "application/json", "Origin": base})
    with urllib.request.urlopen(login_request, timeout=5) as login:
        cookie = login.headers.get("Set-Cookie", "")
        assert login.status == 200 and "HttpOnly" in cookie and "SameSite=Strict" in cookie
        manifest["login"] = "PASS: actual Docker loopback session, HttpOnly/SameSite=Strict; cookie value not recorded"
    manifest["sqlite_mode_uid"] = command("docker", "exec", cid, "stat", "-c", "%a %u", "/app/data/facts.sqlite")
    assert manifest["sqlite_mode_uid"] == "600 10001", manifest
    legacy = json.dumps([{"id": "smoke-legacy", "status": "done", "received_at": "2026-10-06T00:00:00Z", "diagnosis": "Owned historical smoke fixture"}])
    subprocess.run(["docker", "exec", "-i", cid, "sh", "-c", "cat > /app/data/smoke-legacy.json"], input=legacy, text=True, check=True)
    manifest["import"] = json.loads(command("docker", "exec", cid, "workspacectl", "import-legacy", "--config", "/app/config/config_template.json",
                                          "--legacy", "/app/data/smoke-legacy.json", "--apply"))
    assert manifest["import"]["imported"] == 1
    command("docker", "stop", "-t", "10", cid)
    command("docker", "start", cid)
    for _ in range(60):
        try:
            if request("/ping")[0] == 200:
                break
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(.5)
    else:
        raise RuntimeError("same isolated SQLite layer did not reopen")
    status, body = request("/api/v1/documents", "isolated-smoke-console")
    assert status == 200 and json.loads(body)["data"] == []
    status, body = request("/api/v1/runs/smoke-legacy", "isolated-smoke-console")
    assert status == 200 and json.loads(body)["data"]["id"] == "smoke-legacy"
    manifest["restart"] = "PASS: same SQLite writable layer retains imported ID; failed upload never created document"
    manifest["result"] = "PASS"
finally:
    if cid:
        logs = subprocess.run(["docker", "logs", cid], capture_output=True, text=True)
        (out / "logs/container-smoke.log").write_text(logs.stdout + logs.stderr)
        label = command("docker", "inspect", "--format", '{{index .Config.Labels "oncall.smoke.owner"}}', cid)
        assert label == owner
        command("docker", "rm", "-f", cid)
        manifest["cleanup"] = "removed exact matching owner container; no volume used"
    (out / "container-smoke.json").write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n")
