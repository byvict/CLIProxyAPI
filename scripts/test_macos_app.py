import importlib.util
import io
import json
from pathlib import Path
import plistlib
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('macos_app', Path(__file__).with_name('macos-app.py'))
app = importlib.util.module_from_spec(spec)
spec.loader.exec_module(app)


class DeploymentTests(unittest.TestCase):
    def release(self, root, name):
        release = root / 'releases' / name
        release.mkdir(parents=True)
        for filename in ('cli-proxy-api', 'subscription-monitor', 'management.html'):
            (release / filename).write_text(name + filename)
        info = {'commit': name, 'version': '8.0.44-local.1',
                'sha256': {filename: app.digest(release / filename) for filename in
                           ('cli-proxy-api', 'subscription-monitor', 'management.html')}}
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
                    app.activate(root, root, candidate)
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
                    app.activate(root, root, candidate)
            self.assertEqual(observed, [candidate, previous])
            self.assertEqual((root / 'current').resolve(), previous)

    def test_first_install_failure_stops_both_agents(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            candidate = self.release(root, 'candidate')
            with patch.object(app, 'restart'), patch.object(app, 'wait_health', side_effect=RuntimeError('failed')), \
                    patch.object(app, 'run') as run:
                with self.assertRaisesRegex(RuntimeError, 'failed'):
                    app.activate(root, root, candidate)
            self.assertFalse((root / 'current').exists())
            self.assertEqual(run.call_count, 2)
            self.assertTrue(all(call.args[1] == 'bootout' for call in run.call_args_list))

    def test_success_keeps_previous_release_and_updates_shortcuts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            monitor = root / 'monitor'
            monitor.mkdir()
            (root / 'Iniciar.command').write_text('legacy tmux command')
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            with patch.object(app, 'restart'), patch.object(app, 'wait_health', return_value=[]), \
                    patch.object(app.HTTP, 'open', return_value=io.BytesIO((candidate / 'management.html').read_bytes())):
                app.activate(root, monitor, candidate)
            self.assertEqual((root / 'current').resolve(), candidate)
            self.assertEqual((root / 'previous').resolve(), previous)
            self.assertEqual((root / 'Iniciar.command.before-launchd').read_text(), 'legacy tmux command')
            self.assertIn('/bin/launchctl', (root / 'Iniciar.command').read_text())
            self.assertIn(app.MONITOR_LABEL, (monitor / 'Iniciar.command').read_text())

    def test_wrong_served_panel_rolls_back_even_when_health_matches(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            previous = self.release(root, 'previous')
            candidate = self.release(root, 'candidate')
            app.atomic_link(root / 'current', previous)
            with patch.object(app, 'restart'), patch.object(app, 'wait_health'), \
                    patch.object(app.HTTP, 'open', return_value=io.BytesIO(b'old HTML')):
                with self.assertRaisesRegex(RuntimeError, 'panel differs'):
                    app.activate(root, root, candidate)
            self.assertEqual((root / 'current').resolve(), previous)

    def test_stale_process_is_not_accepted_as_a_successful_deploy(self):
        info = {'commit': 'new', 'version': '8.0.44-local.1'}
        with patch.object(app, 'health', return_value=dict(info, status='ok', commit='old')):
            with self.assertRaisesRegex(RuntimeError, 'do not match'):
                app.wait_health(info, attempts=1)
        with patch.object(app, 'health', return_value=dict(info, status='ok')):
            self.assertEqual(len(app.wait_health(info, attempts=1)), 2)

    def test_both_agents_restart_and_preserve_runtime_locations(self):
        root = Path('/Volumes/Files & Data/proxy')
        monitor = Path('/Volumes/Files & Data/monitor')
        for label, directory, args, env in app.launch_agents(root, monitor):
            config = plistlib.loads(app.plist(label, directory, args, env))
            self.assertTrue(config['RunAtLoad'])
            self.assertTrue(config['KeepAlive'])
            self.assertEqual(config['WorkingDirectory'], str(directory))
            self.assertTrue(all(Path(arg).is_absolute() for arg in args if arg.startswith('/')))
            if label == app.LABEL:
                self.assertEqual(env['MANAGEMENT_STATIC_PATH'], str(root / 'current/management.html'))
                self.assertIn(str(root / 'config.yaml'), args)
            else:
                self.assertIn(str(monitor), args)
                self.assertIn('CLAUDE_CODE_OAUTH_TOKEN', args)

    def test_deploy_requires_clean_pushed_main(self):
        for responses, error in [(['topic'], 'requires main'), (['main', ' M file'], 'Commit all'),
                                 (['main', '', '', 'new', 'old'], 'Push main')]:
            with self.subTest(error=error), patch.object(app, 'git', side_effect=responses):
                with self.assertRaisesRegex(RuntimeError, error):
                    app.source_identity()


if __name__ == '__main__':
    unittest.main()
