import importlib.util
import io
import json
import os
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('macos_app', Path(__file__).with_name('macos-app.py'))
app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(app)


class DeploymentTests(unittest.TestCase):
    def release(self, root, name, artifacts=app.ARTIFACTS):
        release = root / 'releases' / name
        release.mkdir(parents=True)
        for filename in artifacts:
            (release / filename).write_text(name + filename)
        info = {'commit': name, 'version': '8.0.44-local.1',
                'sha256': {filename: app.digest(release / filename) for filename in artifacts}}
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
                    patch.object(app, 'wait_health', side_effect=[RuntimeError('wrong commit'), []]), \
                    patch.object(app, 'install_agent') as install:
                with self.assertRaisesRegex(RuntimeError, 'wrong commit'):
                    app.activate(root, candidate)
            install.assert_not_called()
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
                    patch.object(app.HTTP, 'open', return_value=io.BytesIO((candidate / 'management.html').read_bytes())), \
                    patch.object(app, 'install_agent') as install:
                app.activate(root, candidate)
            install.assert_called_once_with(app.IMPORT_LABEL, app.import_plist(root))
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

    def test_import_job_runs_the_active_release_importer_every_15_minutes(self):
        root = Path('/Users/someone/Dev/CLIProxyAPI/app')
        config = plistlib.loads(app.import_plist(root))
        self.assertEqual(config['Label'], app.IMPORT_LABEL)
        self.assertEqual(config['ProgramArguments'], [
            '/usr/bin/python3', '-B', str(root / 'current/import-performance-history.py'),
            '--destination', str(root / 'performance-history.sqlite'),
            '--receipt', str(app.LOGS / 'performance-import.json')])
        self.assertTrue(config['RunAtLoad'])
        self.assertEqual(config['StartInterval'], 900)
        self.assertNotIn('KeepAlive', config)
        self.assertIn('/opt/homebrew/bin', config['EnvironmentVariables']['PATH'].split(':'))
        self.assertEqual(config['StandardErrorPath'], str(app.LOGS / 'performance-import.stderr.log'))

    def test_release_checksums_cover_the_importer_and_older_manifests_stay_valid(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            candidate = self.release(root, 'candidate')
            app.check_release(candidate)
            (candidate / 'import-performance-history.py').write_text('tampered')
            with self.assertRaisesRegex(RuntimeError, 'checksum mismatch: import-performance-history.py'):
                app.check_release(candidate)
            app.check_release(self.release(root, 'older', ('cli-proxy-api', 'management.html')))

    def test_stop_and_uninstall_cover_proxy_and_import_jobs(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory).resolve()
            runtime = home / 'runtime'
            runtime.mkdir()
            (runtime / 'config.yaml').write_text('management:\n  disable-auto-update-panel: true\n')
            agents = home / 'Library/LaunchAgents'
            agents.mkdir(parents=True)
            plists = [agents / (label + '.plist') for label in (app.LABEL, app.IMPORT_LABEL)]
            for path in plists:
                path.write_bytes(b'')
            for command, remaining in [('stop', plists), ('uninstall', [])]:
                with self.subTest(command=command), patch.object(app.Path, 'home', return_value=home), \
                        patch.object(app, 'run') as run, patch('sys.stdout', new=io.StringIO()), \
                        patch('sys.argv', ['macos-app.py', command, '--runtime', str(runtime)]):
                    app.main()
                    booted = [call.args[2] for call in run.call_args_list if call.args[1] == 'bootout']
                    self.assertEqual(booted, [f'gui/{os.getuid()}/{app.LABEL}', f'gui/{os.getuid()}/{app.IMPORT_LABEL}'])
                    self.assertEqual(sorted(agents.iterdir()), sorted(remaining))

    def test_deploy_requires_clean_pushed_main(self):
        for responses, error in [(['topic'], 'requires main'), (['main', ' M file'], 'Commit all'),
                                 (['main', '', '', 'new', 'old'], 'Push main')]:
            with self.subTest(error=error), patch.object(app, 'git', side_effect=responses):
                with self.assertRaisesRegex(RuntimeError, error):
                    app.source_identity()


if __name__ == '__main__':
    unittest.main()
