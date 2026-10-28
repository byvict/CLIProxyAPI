"""Canonical files, revision selection and one nonblocking writer per data root."""

import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
import re
import tempfile
from pathlib import Path

VERSION = "t3-observatory/1.0"
GENERATION = re.compile(r"[0-9a-f]{24}")


def timestamp(value):
    parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None or parsed.utcoffset() != dt.timedelta(0):
        raise ValueError("Timestamps must have an explicit UTC offset")
    return parsed


def canonical(value):
    return json.dumps(
        value, sort_keys=True, ensure_ascii=False, allow_nan=False
    ).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


def file_digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def dump(path, value):
    path.write_text(
        json.dumps(value, indent=2, sort_keys=True, ensure_ascii=False, allow_nan=False)
        + "\n",
        encoding="utf-8",
    )


def jsonl(path, rows):
    path.write_bytes(b"".join(canonical(row) + b"\n" for row in rows))


def load(path):
    return json.loads(path.read_text(encoding="utf-8"))


def load_rows(path):
    if not path.exists():
        return []
    return [
        json.loads(line)
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]


def latest(rows, key):
    selected = {}
    for row in rows:
        ident = row[key]
        revision = row.get("revision", 1)
        if not isinstance(ident, str) or not ident:
            raise ValueError("Missing stable " + key)
        prior = selected.get(ident)
        if prior is None or revision > prior.get("revision", 1):
            selected[ident] = row
        elif revision == prior.get("revision", 1) and row != prior:
            raise ValueError("Conflicting equal revision for " + key + ": " + ident)
    return [selected[key] for key in sorted(selected)]


def safe_directory(path, create=False):
    path = Path(os.path.abspath(path.expanduser()))
    for component in [*reversed(path.parents), path]:
        if component.is_symlink():
            raise ValueError("Symlink directory refused: " + str(component))
    if create:
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not path.is_dir():
        raise ValueError("Directory does not exist: " + str(path))
    return path


def regular_file(path):
    safe_directory(path.parent)
    if path.is_symlink() or not path.is_file():
        raise ValueError("Regular file required: " + str(path))
    return path


@contextlib.contextmanager
def writer(data_dir):
    safe_directory(Path(os.path.abspath(data_dir.expanduser())).parent)
    root = safe_directory(data_dir, create=True)
    lock_path = root / ".writer.lock"
    if lock_path.is_symlink():
        raise ValueError("Symlink lock refused")
    with lock_path.open("a+") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise ValueError(
                "Another observatory writer owns this data directory"
            ) from exc
        try:
            yield root
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)


def atomic_bytes(path, content):
    safe_directory(path.parent, create=True)
    if path.is_symlink():
        raise ValueError("Symlink target refused")
    descriptor, temporary = tempfile.mkstemp(prefix=".pending-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def immutable_bytes(path, content):
    safe_directory(path.parent, create=True)
    if path.exists() or path.is_symlink():
        if regular_file(path).read_bytes() != content:
            raise ValueError("Immutable file differs: " + str(path))
        return
    atomic_bytes(path, content)


def publication_file(root, publication):
    generation = publication.get("generation_id", "")
    if not isinstance(generation, str) or not GENERATION.fullmatch(generation):
        raise ValueError("Invalid generation ID")
    expected = "export/" + generation + "/dashboard.json"
    if publication.get("file") != expected:
        raise ValueError("Publication path must be exactly " + expected)
    return regular_file(root / expected)
