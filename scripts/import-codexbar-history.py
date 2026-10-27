#!/usr/bin/env python3
"""Import recorded CodexBar quota readings, keeping unidentified accounts separate."""

import argparse
import contextlib
import datetime as dt
import hashlib
import json
import math
import os
import sqlite3
import urllib.request
from pathlib import Path


def observations(root, identities):
    rows = []
    accounts = []
    for provider in ("claude", "codex"):
        path = root / (provider + ".json")
        if not path.is_file():
            continue
        data = json.loads(path.read_text())
        if data.get("version") != 1:
            raise ValueError(f"Unsupported CodexBar history: {path}")
        for account, windows in data.get("accounts", {}).items():
            # A hash of an unknown identity is not evidence of the current login.
            credential = identities.get(account, "codexbar:" + hashlib.sha256((provider + account).encode()).hexdigest()[:16])
            accounts.append({"provider": provider, "credential": credential, "matched": account in identities})
            for window in windows:
                minutes = window["windowMinutes"]
                target = {("claude", 300): "five-hour", ("claude", 10080): "seven-day",
                          ("codex", 300): "five-hour", ("codex", 10080): "weekly"}.get((provider, minutes))
                if target is None:
                    target = "codexbar:" + str(minutes) + ":" + window["name"]
                for entry in window["entries"]:
                    used = entry["usedPercent"]
                    if not isinstance(used, (int, float)) or not math.isfinite(used) or not 0 <= used <= 100:
                        raise ValueError("Invalid recorded CodexBar percentage")
                    at = int(dt.datetime.fromisoformat(entry["capturedAt"].replace("Z", "+00:00")).timestamp() * 1000)
                    reset = entry.get("resetsAt")
                    reset_ms = int(dt.datetime.fromisoformat(reset.replace("Z", "+00:00")).timestamp() * 1000) if reset else None
                    rows.append((credential, provider, target, "CodexBar · " + window["name"], at, 100 - used, reset_ms, minutes / 60))
    return rows, accounts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--auth-dir", type=Path, required=True)
    parser.add_argument("--proxy-url", required=True)
    parser.add_argument("--management-key-file", type=Path, required=True)
    parser.add_argument("--receipt", type=Path)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    request = urllib.request.Request(args.proxy_url.rstrip("/") + "/v8/management/credentials",
        headers={"Authorization": "Bearer " + args.management_key_file.read_text().strip()})
    with urllib.request.urlopen(request) as response:
        files = json.load(response)["files"]
    indices = {f["name"]: str(f.get("auth_index", f.get("authIndex", ""))) for f in files}
    identities = {}
    for path in args.auth_dir.glob("*.json"):
        auth = json.loads(path.read_text())
        if auth.get("type") == "codex" and auth.get("account_id") and indices.get(path.name):
            identities["codex:v1:provider-account:" + auth["account_id"]] = indices[path.name]
    rows, accounts = observations(args.source, identities)
    result = {"dryRun": args.dry_run, "observations": len(rows), "accounts": accounts,
              "firstObservedMs": min(r[4] for r in rows) if rows else None}
    if not args.dry_run:
        if not args.destination.is_file():
            parser.error("Initialize the quota history database before importing")
        with contextlib.closing(sqlite3.connect(args.destination)) as destination:
            destination.execute("PRAGMA busy_timeout=5000")
            backup = args.destination.with_suffix(".before-codexbar-" + dt.datetime.now().strftime("%Y%m%d%H%M%S") + ".sqlite")
            with contextlib.closing(sqlite3.connect(backup)) as copy:
                destination.backup(copy)
            before = destination.total_changes
            with destination:
                destination.executemany("INSERT OR IGNORE INTO observations VALUES (?,?,?,?,?,?,?,?)", rows)
            result["inserted"] = destination.total_changes - before
            result["backup"] = str(backup)
            result["integrity"] = destination.execute("PRAGMA quick_check").fetchone()[0]
    if args.receipt:
        args.receipt.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
