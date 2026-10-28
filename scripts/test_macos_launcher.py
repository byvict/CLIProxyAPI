import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest


@unittest.skipUnless(sys.platform == 'darwin', 'macOS launcher')
class LauncherTests(unittest.TestCase):
    def test_forwards_sigterm_and_preserves_proxy_runtime(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            launcher = root / 'launcher'
            repo = Path(__file__).resolve().parent.parent
            subprocess.run(['go', 'build', '-o', str(launcher), './cmd/macos-launcher'], cwd=repo, check=True)
            runtime = root / 'runtime'
            (runtime / 'current').mkdir(parents=True)
            proxy = runtime / 'current/cli-proxy-api'
            proxy.write_text('''#!/usr/bin/python3
import json, os, signal, sys
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
print(json.dumps({'cwd': os.getcwd(), 'args': sys.argv[1:], 'panel': os.environ['MANAGEMENT_STATIC_PATH']}), flush=True)
signal.pause()
''')
            proxy.chmod(0o700)
            with subprocess.Popen([str(launcher), str(runtime)], stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True) as process:
                try:
                    observed = json.loads(process.stdout.readline())
                    self.assertEqual(observed['cwd'], str(runtime))
                    self.assertEqual(observed['args'], ['--config', str(runtime / 'config.yaml')])
                    self.assertEqual(observed['panel'], str(runtime / 'current/management.html'))
                    process.send_signal(signal.SIGTERM)
                    process.communicate(timeout=10)
                    self.assertEqual(process.returncode, 0)
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.wait()


if __name__ == '__main__':
    unittest.main()
