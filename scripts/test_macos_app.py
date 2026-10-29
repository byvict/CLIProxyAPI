import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('macos_app', Path(__file__).with_name('macos-app.py'))
app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(app)


class DeploymentTests(unittest.TestCase):
    def release(self, root, name):
        release = root / 'releases' / name
        release.mkdir(parents=True)
        for filename in ('cli-proxy-api', 'management.html'):
            (release / filename).write_text(name + filename)
        info = {'commit': name, 'version': '8.0.44-local.1',
                'sha256': {filename: app.digest(release / filename) for filename in
                           ('cli-proxy-api', 'management.html')}}
        (release / 'release.json').write_text(json.dumps(info))
        return release

    def test_corrupted_release_leaves_running_version_untouched(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            (candidate / 'management.html').write_text('corrupted')
            with patch.object(app, 'restart') as restart:
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    app.activate(root, candidate)
                restart.assert_not_called()
            self.assertEqual((root / 'current').resolve(), previous)

    def test_failed_health_restores_previous_release(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            observed = []
            with patch.object(app, 'restart', side_effect=lambda *_: observed.append((root / 'current').resolve())), \
                    patch.object(app, 'wait_health', side_effect=[RuntimeError('wrong commit'), []]):
                with self.assertRaisesRegex(RuntimeError, 'wrong commit'):
                    app.activate(root, candidate)
            self.assertEqual(observed, [candidate, previous])
            self.assertEqual((root / 'current').resolve(), previous)

    def test_first_install_failure_stops_only_proxy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            candidate = self.release(root, 'candidate')
            with patch.object(app, 'restart'), patch.object(app, 'wait_health', side_effect=RuntimeError('failed')), \
                    patch.object(app, 'run') as run:
                with self.assertRaisesRegex(RuntimeError, 'failed'):
                    app.activate(root, candidate)
            self.assertFalse((root / 'current').exists())
            self.assertEqual(run.call_count, 1)
            self.assertTrue(all(call.args[1] == 'bootout' for call in run.call_args_list))

    def test_success_keeps_previous_release_and_updates_shortcuts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            (root / 'Iniciar.command').write_text('legacy tmux command')
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            with patch.object(app, 'restart'), patch.object(app, 'wait_health', return_value=[]), \
                    patch.object(app.HTTP, 'open', return_value=io.BytesIO((candidate / 'management.html').read_bytes())):
                app.activate(root, candidate)
            self.assertEqual((root / 'current').resolve(), candidate)
            self.assertEqual((root / 'previous').resolve(), previous)
            self.assertEqual((root / 'Iniciar.command.before-launchd').read_text(), 'legacy tmux command')
            self.assertIn('/bin/launchctl', (root / 'Iniciar.command').read_text())

    def test_wrong_served_panel_rolls_back_even_when_health_matches(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            with patch.object(app, 'restart'), patch.object(app, 'wait_health'), \
                    patch.object(app.HTTP, 'open', return_value=io.BytesIO(b'old HTML')):
                with self.assertRaisesRegex(RuntimeError, 'panel differs'):
                    app.activate(root, candidate)
            self.assertEqual((root / 'current').resolve(), previous)

    def test_stale_process_is_not_accepted_as_a_successful_deploy(self):
        info = {'commit': 'new', 'version': '8.0.44-local.1'}
        with patch.object(app, 'health', return_value=dict(info, status='ok', commit='old')):
            with self.assertRaisesRegex(RuntimeError, 'does not match'):
                app.wait_health(info, attempts=1)
        with patch.object(app, 'health', return_value=dict(info, status='ok')):
            self.assertEqual(app.wait_health(info, attempts=1)['commit'], 'new')

    def test_service_uses_stable_launcher_and_keeps_runtime_on_ssd(self):
        root = Path('/Volumes/Files & Data/proxy')
        config = plistlib.loads(app.plist(root))
        self.assertTrue(config['RunAtLoad'])
        self.assertTrue(config['KeepAlive'])
        self.assertEqual(config['ProgramArguments'], [str(app.LAUNCHER), str(root)])
        self.assertEqual(config['WorkingDirectory'], str(Path.home()))
        self.assertEqual(config['Label'], app.LABEL)
        self.assertEqual(config['StandardErrorPath'], str(app.LOGS / 'launchd.stderr.log'))

    def test_go_tests_run_as_launchd_job_and_fail_deploy_on_nonzero_exit(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory)
            jobs = []

            def fake_run(*args, capture=False, check=True, cwd=app.REPO):
                if args[1] == 'bootstrap':
                    jobs.append(plistlib.loads(Path(args[3]).read_bytes()))
                    (home / 'logs' / 'deploy-tests.log').write_text('FAIL\tpkg\n')
                if args[1] == 'print':
                    return SimpleNamespace(stdout='\tstate = not running\n\tlast exit code = 1\n')
                return SimpleNamespace(stdout='')

            with patch.object(app.Path, 'home', return_value=home), patch.object(app, 'LOGS', home / 'logs'), \
                    patch.object(app, 'run', side_effect=fake_run), \
                    patch.dict(os.environ, {'PATH': '/toolchain', 'SECRET_TOKEN': 'x'}), \
                    patch('sys.stdout', new=io.StringIO()) as output:
                with self.assertRaisesRegex(RuntimeError, 'exit code 1'):
                    app.run_in_launchd('/toolchain/go', 'test', './...')
            self.assertEqual(jobs[0]['ProgramArguments'], ['/toolchain/go', 'test', './...'])
            self.assertEqual(jobs[0]['WorkingDirectory'], str(app.REPO))
            self.assertFalse(jobs[0]['KeepAlive'])
            self.assertEqual(jobs[0]['EnvironmentVariables']['PATH'], '/toolchain')
            self.assertNotIn('SECRET_TOKEN', jobs[0]['EnvironmentVariables'])
            self.assertIn('FAIL\tpkg', output.getvalue())
            self.assertEqual(list((home / 'Library/LaunchAgents').iterdir()), [])

    def test_deploy_requires_clean_pushed_main(self):
        for responses, error in [(['topic'], 'requires main'), (['main', ' M file'], 'Commit all'),
                                 (['main', '', '', 'new', 'old'], 'Push main')]:
            with self.subTest(error=error), patch.object(app, 'git', side_effect=responses):
                with self.assertRaisesRegex(RuntimeError, error):
                    app.source_identity()


if __name__ == '__main__':
    unittest.main()
