#!/usr/bin/env python3
"""Import v1 observations only when the proxy credential's identity matches exactly."""

import argparse
import base64
import datetime
import hashlib
import json
import math
import re
import sqlite3
import urllib.request
from collections import Counter
from pathlib import Path


def claims(token):
    try:
        payload = token.split(".")[1]
        return json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    except (ValueError, IndexError):
        return {}


def identity(credential):
    provider = {"xai": "grok", "meta": "muse"}.get(credential["type"], credential["type"])
    basis = "provider_account"
    if provider == "claude":
        subject = credential.get("account_uuid", "")
    elif provider == "muse":
        subject = credential.get("email", "").strip().lower()
        basis = "provider_email"
    elif provider in ("codex", "grok"):
        token = claims(credential.get("access_token", ""))
        account = credential.get("account_id" if provider == "codex" else "sub", "")
        parts = [token.get("iss", ""), account, token.get("sub", "")]
        subject = json.dumps(parts, separators=(",", ":"), ensure_ascii=False) if all(parts) else ""
    else:
        subject = ""
    if not subject:
        return None
    return hashlib.sha256(f"{provider}\0{basis}\0{subject}".encode()).hexdigest()[:32]


def window_id(provider, key, native_id):
    if key["scope"] != "all":
        return "seven-day-fable" if provider == "claude" and key["scope"].lower() == "provider-label:fable" else f"v1:{native_id}"
    if provider == "claude":
        return {"session": "five-hour", "weekly": "seven-day"}.get(key["kind"], f"v1:{native_id}")
    if provider == "muse":
        return {"session": "window", "weekly": "weekly"}.get(key["kind"], f"v1:{native_id}")
    return key["kind"] if key["kind"] in ("weekly", "monthly") else f"v1:{native_id}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--auth-dir", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--proxy-url", required=True)
    parser.add_argument("--management-key-file", type=Path, required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    if args.source.resolve() == args.destination.resolve():
        parser.error("Source and destination must differ")
    request = urllib.request.Request(args.proxy_url.rstrip("/") + "/v8/management/credentials",
        headers={"Authorization": "Bearer " + args.management_key_file.read_text().strip()})
    with urllib.request.urlopen(request) as response:
        files = json.load(response)["files"]
    indices = {file["name"]: str(file.get("auth_index", file.get("authIndex", ""))) for file in files}
    source = sqlite3.connect(args.source.resolve().as_uri() + "?mode=ro", uri=True)
    source.execute("BEGIN")
    accounts = {row[0] for row in source.execute("SELECT id FROM accounts")}
    mappings = {}
    unmatched = Counter()
    for path in args.auth_dir.glob("*.json"):
        credential = json.loads(path.read_text())
        account = identity(credential)
        if account in accounts and indices.get(path.name):
            mappings[account] = (indices[path.name], credential["type"])
        else:
            unmatched[credential.get("type", "unknown")] += 1
    observations = []
    series = {}
    latest_basis = {}
    rows = list(source.execute("SELECT account_id, window_id, at_ns, reading FROM readings ORDER BY at_ns"))
    for account, native_id, at_ns, payload in rows:
        if account not in mappings:
            continue
        reading = json.loads(payload)
        key = reading["key"]
        latest_basis[(account, window_id({"xai": "grok", "meta": "muse"}.get(mappings[account][1], mappings[account][1]), key, native_id))] = key["capacityBasis"]
    for account, native_id, at_ns, payload in rows:
        if account not in mappings:
            continue
        credential, provider = mappings[account]
        reading = json.loads(payload)
        key = reading["key"]
        if key["unit"] != "percent" or reading["capacity"] != 100:
            continue
        native_provider = {"xai": "grok", "meta": "muse"}.get(provider, provider)
        target_window = window_id(native_provider, key, native_id)
        if latest_basis[(account, target_window)] != key["capacityBasis"]:
            target_window = f"v1:{native_id}"
        balance = reading["remaining"]
        if balance is not None and (not math.isfinite(balance) or not 0 <= balance <= 100):
            raise ValueError("Invalid historical percentage")
        reset = reading.get("resetsAt")
        normalized_reset = re.sub(r"\.(\d+)", lambda match: "." + match[1][:6].ljust(6, "0"), reset).replace("Z", "+00:00") if reset else None
        reset_ms = int(datetime.datetime.fromisoformat(normalized_reset).timestamp() * 1000) if normalized_reset else None
        row = (credential, provider, target_window, reading["label"], at_ns // 1_000_000,
               balance, reset_ms, key["periodSeconds"] / 3600 or None)
        observations.append(row)
        series[(account, target_window)] = row
    # Retain failure and signed-out barriers so imports cannot connect across them.
    for account, at_ns in source.execute("SELECT account_id, at_ns FROM attempts WHERE status <> 'ok' AND account_id IS NOT NULL UNION SELECT account_id, to_ns FROM source_bindings WHERE to_ns IS NOT NULL"):
        for (series_account, _), last in series.items():
            if account == series_account:
                observations.append((*last[:4], at_ns // 1_000_000, None, last[6], last[7]))
    counts = Counter(row[1] for row in observations)
    result = {"matchedCredentials": len(mappings), "unmatchedProviders": dict(unmatched),
              "observationsByProvider": dict(counts), "dryRun": args.dry_run}
    if not args.dry_run:
        if not args.destination.is_file():
            parser.error("Initialize the destination through the proxy before importing")
        destination = sqlite3.connect(args.destination)
        backup = args.destination.with_name(args.destination.stem + ".before-v1-" + datetime.datetime.now().strftime("%Y%m%d%H%M%S") + ".sqlite")
        with sqlite3.connect(backup) as copy:
            destination.backup(copy)
        backup.chmod(0o600)
        with destination:
            destination.executemany("""INSERT OR IGNORE INTO observations
                (credential, provider, window_id, label, observed_at_ms, remaining, reset_at_ms, period_hours)
                VALUES (?,?,?,?,?,?,?,?)""", observations)
        result["backup"] = str(backup)
        destination.close()
    source.close()
    print(json.dumps(result))


if __name__ == "__main__":
    main()
