#!/usr/bin/env python3
"""Reassociate quota observations after a runtime move. Dry-run unless --apply is supplied."""
import argparse
import hashlib
import json
from pathlib import Path
import sqlite3


def aliases_for(auth_dir, old_auth_dirs):
    aliases = {}
    for path in sorted(auth_dir.rglob('*.json')):
        metadata = json.loads(path.read_text())
        kind = str(metadata.get('type', '')).strip().lower()
        if not kind:
            continue
        provider = 'gemini-cli' if kind == 'gemini' else kind
        relative = path.relative_to(auth_dir).as_posix()
        stable = 'file:' + hashlib.sha256(f'{provider}:{relative}'.encode()).hexdigest()[:32]
        for directory in [auth_dir, *old_auth_dirs]:
            old = hashlib.sha256(f'{kind}:{directory / relative}'.encode()).hexdigest()[:16]
            aliases[old] = stable
    return aliases


def migrate(database, aliases, apply=False):
    db = sqlite3.connect(database)
    try:
        db.execute('BEGIN IMMEDIATE')
        changed = sum(db.execute('SELECT count(*) FROM observations WHERE credential=?', (old,)).fetchone()[0]
                      for old in aliases)
        backup = database.with_name(database.stem + '.before-identity.sqlite')
        if apply and changed and not backup.exists():
            with sqlite3.connect(f'file:{database}?mode=ro', uri=True) as source, sqlite3.connect(backup) as target:
                source.backup(target)
            backup.chmod(0o600)
        polls = db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='polls'").fetchone()
        before = db.execute('SELECT count(*) FROM observations').fetchone()[0]
        for old, stable in aliases.items():
            conflict = db.execute('''SELECT count(*) FROM observations a JOIN observations b
                ON b.credential=? AND a.window_id=b.window_id AND a.observed_at_ms=b.observed_at_ms
                WHERE a.credential=? AND (a.provider IS NOT b.provider OR a.label IS NOT b.label OR
                a.remaining IS NOT b.remaining OR a.reset_at_ms IS NOT b.reset_at_ms OR a.period_hours IS NOT b.period_hours)''',
                (stable, old)).fetchone()[0]
            if conflict:
                raise ValueError(f'Conflicting observations: {conflict}; migration rolled back')
            db.execute('''INSERT OR IGNORE INTO observations SELECT ?, provider, window_id, label,
                observed_at_ms, remaining, reset_at_ms, period_hours FROM observations WHERE credential=?''', (stable, old))
            db.execute('DELETE FROM observations WHERE credential=?', (old,))
            if polls:
                db.execute('''INSERT INTO polls SELECT ?, next_at_ms FROM polls WHERE credential=?
                    ON CONFLICT(credential) DO UPDATE SET next_at_ms=max(polls.next_at_ms,excluded.next_at_ms)''', (stable, old))
                db.execute('DELETE FROM polls WHERE credential=?', (old,))
        after = db.execute('SELECT count(*) FROM observations').fetchone()[0]
        if apply:
            db.commit()
        else:
            db.rollback()
        return {'applied': apply, 'reassociated': changed, 'rowsBefore': before, 'rowsAfter': after,
                'backup': str(backup) if apply and changed else None}
    finally:
        db.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--database', type=Path, required=True)
    parser.add_argument('--auth-dir', type=Path, required=True)
    parser.add_argument('--old-auth-dir', type=Path, action='append', default=[])
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if not args.database.is_file() or not args.auth_dir.is_dir():
        parser.error('Existing database and auth directory required')
    aliases = aliases_for(args.auth_dir.absolute(), [p.absolute() for p in args.old_auth_dir])
    print(json.dumps(migrate(args.database, aliases, args.apply), indent=2))


if __name__ == '__main__':
    main()
