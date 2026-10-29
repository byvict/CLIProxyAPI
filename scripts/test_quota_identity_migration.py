import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('quota_migration', Path(__file__).with_name('migrate-quota-history.py'))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class HistoryMigrationTests(unittest.TestCase):
    def test_moves_preserve_observations_and_reservations_idempotently(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            auth = root / 'auths'
            auth.mkdir()
            (auth / 'account.json').write_text(json.dumps({'type': 'codex'}))
            aliases = migration.aliases_for(auth, [root / 'old/auths', root / 'middle/auths'])
            self.assertEqual(len(aliases), 3)
            self.assertEqual(len(set(aliases.values())), 1)
            path = root / 'history.sqlite'
            with sqlite3.connect(path) as db:
                db.execute('CREATE TABLE observations (credential,provider,window_id,label,observed_at_ms,remaining,reset_at_ms,period_hours,PRIMARY KEY(credential,window_id,observed_at_ms))')
                db.execute('CREATE TABLE polls (credential PRIMARY KEY,next_at_ms)')
                for i, old in enumerate(aliases):
                    db.execute('INSERT INTO observations VALUES (?,?,?,?,?,?,?,?)', (old,'codex','weekly','Weekly',i,90-i,100,168))
                    db.execute('INSERT INTO polls VALUES (?,?)', (old,100+i))
            self.assertEqual(migration.migrate(path, aliases)['reassociated'], 3)
            result = migration.migrate(path, aliases, True)
            self.assertEqual(result['rowsBefore'], result['rowsAfter'])
            self.assertTrue(Path(result['backup']).exists())
            self.assertEqual(migration.migrate(path, aliases, True)['reassociated'], 0)
            with sqlite3.connect(path) as db:
                self.assertEqual(db.execute('SELECT count(distinct credential),count(*) FROM observations').fetchone(), (1,3))
                self.assertEqual(db.execute('SELECT next_at_ms FROM polls').fetchall(), [(102,)])
            with sqlite3.connect(result['backup']) as db:
                self.assertEqual(db.execute('SELECT count(distinct credential) FROM observations').fetchone()[0], 3)
