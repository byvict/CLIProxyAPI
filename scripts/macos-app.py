#!/usr/bin/env python3
"""Build and operate the local proxy and subscription monitor with launchd."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REPO = Path(__file__).resolve().parent.parent
LABEL = 'com.byvict.cliproxyapi'
MONITOR_LABEL = LABEL + '.monitor'
RUNTIME = Path('/Volumes/Arquivos/cliproxyapi-original')
MONITOR = Path('/Volumes/Arquivos/cliproxyapi-monitor-v1')
BUILD_ROOT = Path('/Volumes/Arquivos/cliproxyapi-build')


def run(*args, capture=False, check=True, cwd=REPO):
    return subprocess.run(args, cwd=cwd, check=check, text=True,
                          stdout=subprocess.PIPE if capture else None,
                          stderr=subprocess.PIPE if capture else None)


def git(*args):
    return run('git', *args, capture=True).stdout.strip()


def source_identity():
    if git('branch', '--show-current') != 'main':
        raise RuntimeError('Deploy requires main in the definitive checkout.')
    if git('status', '--porcelain'):
        raise RuntimeError('Commit all changes before deployment.')
    git('fetch', 'fork', 'main')
    commit = git('rev-parse', 'HEAD')
    if commit != git('rev-parse', 'fork/main'):
        raise RuntimeError('Push main to fork before deployment; local and remote must match.')
    version = (REPO / 'VERSION').read_text().strip()
    if not re.fullmatch(r'\d+\.\d+\.\d+-local\.\d+', version):
        raise RuntimeError('VERSION must use X.Y.Z-local.N.')
    return {'version': version, 'commit': commit}


def atomic_link(link, target):
    temporary = link.with_name(link.name + '.new')
    temporary.unlink(missing_ok=True)
    temporary.symlink_to(target)
    temporary.replace(link)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check_release(release):
    info = json.loads((release / 'release.json').read_text())
    for name in ('cli-proxy-api', 'subscription-monitor', 'management.html'):
        if digest(release / name) != info['sha256'][name]:
            raise RuntimeError(f'Release checksum mismatch: {name}')
    return info


def build_release(runtime, build_root, info):
    current = runtime / 'current'
    if current.is_symlink():
        previous = check_release(current.resolve())
        if previous['version'] == info['version'] and previous['commit'] != info['commit']:
            raise RuntimeError('Bump VERSION and update CHANGELOG.local.md before a new release.')
    run('python3', '-B', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_macos_app.py')
    run('go', 'test', './...')
    unformatted = run('gofmt', '-l', 'internal/api/server_routes.go',
                      'internal/api/server_test.go', 'internal/submon/httpapi.go',
                      'internal/submon/httpapi_test.go', capture=True).stdout.strip()
    if unformatted:
        raise RuntimeError(f'Run gofmt before deployment: {unformatted}')
    build_root.mkdir(parents=True, exist_ok=True)
    releases = runtime / 'releases'
    releases.mkdir(exist_ok=True)
    destination = releases / f"{info['version']}-{info['commit'][:12]}"
    if destination.exists():
        if check_release(destination)['commit'] != info['commit']:
            raise RuntimeError('Release directory belongs to another commit.')
        return destination
    stage = Path(tempfile.mkdtemp(prefix='.build-', dir=releases))
    try:
        info = dict(info, build_date=time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()))
        flags = ' '.join(f'-X main.{key}={info[field]}' for key, field in
                         [('Version', 'version'), ('Commit', 'commit'), ('BuildDate', 'build_date')])
        run('go', 'build', '-ldflags', flags, '-o', str(stage / 'cli-proxy-api'), './cmd/server')
        package = 'github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo'
        run('go', 'build', '-ldflags', flags.replace('main.', package + '.'),
            '-o', str(stage / 'subscription-monitor'), './cmd/subscription-monitor')
        run('bash', 'scripts/build-management.sh', str(build_root), str(stage / 'management.html'))
        info['sha256'] = {name: digest(stage / name) for name in
                          ('cli-proxy-api', 'subscription-monitor', 'management.html')}
        (stage / 'release.json').write_text(json.dumps(info, indent=2) + '\n')
        stage.rename(destination)
    finally:
        if stage.exists():
            shutil.rmtree(stage)
    return destination


def launch_agents(runtime, monitor):
    current = runtime / 'current'
    return [
        (LABEL, runtime, [str(current / 'cli-proxy-api'), '--config', str(runtime / 'config.yaml')],
         {'MANAGEMENT_STATIC_PATH': str(current / 'management.html')}),
        (MONITOR_LABEL, monitor, ['/usr/bin/env', '-u', 'CLAUDE_CODE_OAUTH_TOKEN',
         '-u', 'ANTHROPIC_API_KEY', '-u', 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR',
         str(current / 'subscription-monitor'), '--data-dir', str(monitor), '--listen', '127.0.0.1:8318'], {}),
    ]


def plist(label, directory, arguments, environment):
    return plistlib.dumps({
        'Label': label, 'ProgramArguments': arguments, 'WorkingDirectory': str(directory),
        'EnvironmentVariables': environment, 'RunAtLoad': True, 'KeepAlive': True,
        'ProcessType': 'Background', 'ThrottleInterval': 10, 'ExitTimeOut': 30,
        'StandardOutPath': str(directory / 'logs' / 'launchd.stdout.log'),
        'StandardErrorPath': str(directory / 'logs' / 'launchd.stderr.log'),
    })


def restart(runtime, monitor):
    domain = f'gui/{os.getuid()}'
    agents_dir = Path.home() / 'Library/LaunchAgents'
    agents_dir.mkdir(parents=True, exist_ok=True)
    for label, directory, arguments, environment in launch_agents(runtime, monitor):
        (directory / 'logs').mkdir(mode=0o700, exist_ok=True)
        path = agents_dir / (label + '.plist')
        content = plist(label, directory, arguments, environment)
        loaded = run('launchctl', 'print', f'{domain}/{label}', capture=True, check=False).returncode == 0
        if loaded and path.exists() and path.read_bytes() == content:
            run('launchctl', 'kickstart', '-k', f'{domain}/{label}')
        else:
            path.write_bytes(content)
            run('plutil', '-lint', str(path))
            run('launchctl', 'bootout', f'{domain}/{label}', capture=True, check=False)
            run('launchctl', 'bootstrap', domain, str(path))


HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def health(port):
    with HTTP.open(f'http://127.0.0.1:{port}/healthz') as response:
        return json.load(response)


def wait_health(info, attempts=45):
    last = 'no response'
    for attempt in range(attempts):
        try:
            readings = [health(port) for port in (8317, 8318)]
            if all(reading.get('status') == 'ok' and all(reading.get(key) == info[key]
                   for key in ('commit', 'version')) for reading in readings):
                return readings
            last = 'running builds do not match the release'
        except (OSError, ValueError, urllib.error.URLError) as error:
            last = str(error)
        if attempt + 1 < attempts:
            time.sleep(1)
    raise RuntimeError(f'Deploy health failed: {last}')


def write_shortcuts(runtime, monitor):
    for label, directory in [(LABEL, runtime), (MONITOR_LABEL, monitor)]:
        target = shlex.quote(f'gui/{os.getuid()}/{label}')
        path = shlex.quote(str(Path.home() / 'Library/LaunchAgents' / (label + '.plist')))
        commands = {
            'Iniciar.command': f'''if ! /bin/launchctl print {target} >/dev/null 2>&1; then
  /bin/launchctl bootstrap gui/$(id -u) {path}
fi
/bin/launchctl kickstart {target}
''',
            'Parar.command': f'/bin/launchctl bootout {target}\n',
        }
        for name, command in commands.items():
            shortcut = directory / name
            backup = directory / (name + '.before-launchd')
            if shortcut.exists() and not backup.exists():
                shutil.copy2(shortcut, backup)
            shortcut.write_text('#!/bin/sh\nset -eu\n' + command)
            shortcut.chmod(0o700)


def activate(runtime, monitor, release):
    info = check_release(release)
    current = runtime / 'current'
    previous = current.resolve() if current.is_symlink() else None
    atomic_link(current, release)
    try:
        restart(runtime, monitor)
        readings = wait_health(info)
        with HTTP.open('http://127.0.0.1:8317/management.html') as response:
            if hashlib.sha256(response.read()).hexdigest() != info['sha256']['management.html']:
                raise RuntimeError('Served management panel differs from the release.')
    except Exception:
        if previous is not None:
            atomic_link(current, previous)
            restart(runtime, monitor)
            wait_health(check_release(previous))
            print('Previous release restored.', file=sys.stderr)
        else:
            for label in (LABEL, MONITOR_LABEL):
                run('launchctl', 'bootout', f'gui/{os.getuid()}/{label}', capture=True, check=False)
            current.unlink(missing_ok=True)
        raise
    if previous is not None and previous != release:
        atomic_link(runtime / 'previous', previous)
    write_shortcuts(runtime, monitor)
    print(json.dumps({'release': str(release), 'health': readings}, indent=2))


def validate_runtime(runtime, monitor):
    for directory, required in [(runtime, 'config.yaml'), (monitor, 'access.key')]:
        if not directory.is_dir() or not (directory / required).is_file():
            raise RuntimeError(f'Existing installation required: {directory / required}')
    # The custom panel must not be overwritten by the upstream asset updater.
    if not re.search(r'^\s*disable-auto-update-panel:\s*true\s*(?:#.*)?$',
                     (runtime / 'config.yaml').read_text(), re.MULTILINE):
        raise RuntimeError('Set management.disable-auto-update-panel: true before deployment.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['deploy', 'status', 'start', 'stop', 'uninstall', 'rollback'])
    parser.add_argument('--runtime', type=Path, default=RUNTIME)
    parser.add_argument('--monitor', type=Path, default=MONITOR)
    parser.add_argument('--build-root', type=Path, default=BUILD_ROOT)
    args = parser.parse_args()
    if sys.platform != 'darwin':
        raise RuntimeError('This command requires macOS.')
    runtime, monitor = args.runtime.resolve(), args.monitor.resolve()
    validate_runtime(runtime, monitor)
    if args.command == 'status':
        for label, port in [(LABEL, 8317), (MONITOR_LABEL, 8318)]:
            result = run('launchctl', 'print', f'gui/{os.getuid()}/{label}', capture=True)
            print(label)
            print('\n'.join(line.strip() for line in result.stdout.splitlines()
                            if line.strip().startswith(('state =', 'pid =', 'last exit code ='))))
            print(json.dumps(health(port)))
        return
    with (runtime / '.deploy.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('Another service operation is running.') from None
        if args.command == 'deploy':
            build_root = args.build_root.resolve()
            toolchain = build_root / 'toolchain-bin-path.txt'
            if toolchain.is_file():
                os.environ['PATH'] = (toolchain.read_text().strip() + ':' +
                    str(build_root / 'bun1.4.3/bun-darwin-aarch64') + ':' + os.environ['PATH'])
            for name, directory in [('GOCACHE', 'gocache'), ('GOPATH', 'gopath'), ('GOMODCACHE', 'gomodcache')]:
                os.environ.setdefault(name, str(build_root / directory))
            info = source_identity()
            release = build_release(runtime, build_root, info)
            if source_identity() != info:
                raise RuntimeError('Source changed during the build; deploy aborted.')
            activate(runtime, monitor, release)
        elif args.command == 'rollback':
            previous = runtime / 'previous'
            if not previous.is_symlink():
                raise RuntimeError('No previous managed release is available.')
            activate(runtime, monitor, previous.resolve())
        elif args.command == 'start':
            activate(runtime, monitor, (runtime / 'current').resolve())
        else:
            for label in (LABEL, MONITOR_LABEL):
                run('launchctl', 'bootout', f'gui/{os.getuid()}/{label}', capture=True, check=False)
                if args.command == 'uninstall':
                    (Path.home() / 'Library/LaunchAgents' / (label + '.plist')).unlink(missing_ok=True)
            print('Services stopped. Runtime data and releases are preserved.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
