#!/usr/bin/env python3
"""Create and seed an isolated management panel fixture. Never use live auth files."""

import argparse
import json
import secrets
import sqlite3
import time
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("action", choices=["create", "seed"])
parser.add_argument("directory", type=Path)
parser.add_argument("--port", type=int, default=18317)
parser.add_argument("--history-hours", type=int, choices=range(1, 25), default=24)
args = parser.parse_args()
root = args.directory
if not root.is_absolute() or root.is_symlink() or root.resolve() != root:
    parser.error("Use an absolute, canonical directory without symlinks")
marker = root / ".quota-history-fixture"

if args.action == "create":
    if root.exists() and any(root.iterdir()):
        parser.error("Create requires an empty directory")
    (root / "auths").mkdir(parents=True)
    (root / "static").mkdir()
    marker.touch()
    key = secrets.token_hex(32)
    (root / "management.key").write_text(key + "\n")
    (root / "management.key").chmod(0o600)
    (root / "config.yaml").write_text(f'''config-version: 8
server:
  host: "127.0.0.1"
  port: {args.port}
management:
  allow-remote: false
  secret-key: "{key}"
  disable-auto-update-panel: true
oauth:
  auth-dir: "{root}/auths"
access:
  api-keys: ["quota-history-fixture-client"]
''')
    (root / "config.yaml").chmod(0o600)
    for name in ["before", "after", "green", "red"]:
        (root / "auths" / f"claude-{name}.json").write_text(json.dumps({
            "type": "claude", "access_token": "synthetic-fixture-token",
            "refresh_token": "", "expired": "2099-01-01T00:00:00Z",
            "email": f"{name}@example.test", "account_uuid": f"fixture-{name}",
        }))
    print(root / "config.yaml")
else:
    if not marker.is_file():
        parser.error("Seed requires a directory created by this fixture tool")
    headers = {"Authorization": "Bearer " + (root / "management.key").read_text().strip()}

    def get(path):
        request = urllib.request.Request(
            f"http://127.0.0.1:{args.port}/v8/management/{path}", headers=headers)
        with urllib.request.urlopen(request) as response:
            return json.load(response)

    files = get("credentials")["files"]
    get("quota-history")
    base = int(time.time() * 1000)
    fixtures = {}
    cases = {"before": (4, 1, "yellow"), "after": (34, 1, "blue"),
             "green": (24, 1, "green"), "red": (20, 0.25, "red")}
    with sqlite3.connect(root / "quota-history.sqlite") as database:
        database.execute("DELETE FROM observations")
        if database.execute("SELECT name FROM sqlite_master WHERE name='polls'").fetchone():
            database.execute("DELETE FROM polls")
        for file in files:
            case = file.get("name", "").removeprefix("claude-").removesuffix(".json")
            if case not in cases:
                continue
            remaining, rate, band = cases[case]
            index = str(file.get("auth_index", file.get("authIndex", "")))
            if not index:
                raise RuntimeError("Fixture credential has no auth index")
            reset = base + 24 * 3_600_000
            fixtures[index] = {"name": file["name"], "remaining": remaining,
                               "rate": rate, "band": band, "reset": reset}
            points = args.history_hours * 12
            database.executemany('''INSERT INTO observations
                (credential, provider, window_id, label, observed_at_ms,
                 remaining, reset_at_ms, period_hours) VALUES (?,?,?,?,?,?,?,?)''', [
                (index, "claude", "seven-day", "7-day limit", base - (points-i)*300_000,
                 remaining + (points-i)*rate/12, reset, 168) for i in range(points+1)])
    if len(fixtures) != 4:
        raise RuntimeError("Expected four synthetic credentials")
    (root / "fixtures.json").write_text(json.dumps({"base": base, "accounts": fixtures}))
    print(json.dumps({"seeded_credentials": len(fixtures), "observations": len(fixtures)*(args.history_hours*12+1)}))
