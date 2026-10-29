#!/usr/bin/env python3
"""Build and operate the local proxy with launchd."""
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
RUNTIME = Path.home() / 'Library/Application Support/CLIProxyAPI'
BUILD_ROOT = Path.home() / 'Library/Caches/CLIProxyAPI-build'


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
    for name in ('cli-proxy-api', 'management.html'):
        if digest(release / name) != info['sha256'][name]:
            raise RuntimeError(f'Release checksum mismatch: {name}')
    return info


def build_release(runtime, build_root, info):
    current = runtime / 'current'
    if current.is_symlink():
        previous = check_release(current.resolve())
        if previous['version'] == info['version'] and previous['commit'] != info['commit']:
            raise RuntimeError('Bump VERSION and update CHANGELOG.local.md before a new release.')
    run('python3', '-B', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_macos_*.py')
    run('go', 'test', './...')
    unformatted = run('gofmt', '-l', 'internal/api/server_routes.go',
                      'internal/api/server_test.go', 'cmd/macos-launcher/main.go', capture=True).stdout.strip()
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
        run('bash', 'scripts/build-management.sh', str(build_root), str(stage / 'management.html'))
        info['sha256'] = {name: digest(stage / name) for name in
                          ('cli-proxy-api', 'management.html')}
        (stage / 'release.json').write_text(json.dumps(info, indent=2) + '\n')
        stage.rename(destination)
    finally:
        if stage.exists():
            shutil.rmtree(stage)
    return destination


LAUNCHER_APP = Path.home() / 'Applications/CLIProxyAPI Service.app'
LAUNCHER = LAUNCHER_APP / 'Contents/MacOS/cliproxyapi-service'
LOGS = Path.home() / 'Library/Logs/CLIProxyAPI'
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def plist(runtime):
    return plistlib.dumps({
        'Label': LABEL, 'ProgramArguments': [str(LAUNCHER), str(runtime)],
        'WorkingDirectory': str(Path.home()),
        'AssociatedBundleIdentifiers': ['com.byvict.cliproxyapi.service'],
        'RunAtLoad': True, 'KeepAlive': True,
        'ProcessType': 'Background', 'ThrottleInterval': 10, 'ExitTimeOut': 30,
        'StandardOutPath': str(LOGS / 'launchd.stdout.log'),
        'StandardErrorPath': str(LOGS / 'launchd.stderr.log'),
    })


def check_access(runtime):
    if not LAUNCHER.is_file():
        raise RuntimeError('Install the launcher with scripts/install-macos-launcher.sh first.')
    LOGS.mkdir(parents=True, mode=0o700, exist_ok=True)
    label = LABEL + '.access-check'
    path = Path.home() / 'Library/LaunchAgents' / (label + '.plist')
    path.parent.mkdir(parents=True, exist_ok=True)
    output, errors = LOGS / 'access-check.stdout.log', LOGS / 'access-check.stderr.log'
    output.write_text('')
    errors.write_text('')
    config = plistlib.loads(plist(runtime))
    config.update(Label=label, ProgramArguments=[str(LAUNCHER), '--check', str(runtime)],
                  KeepAlive=False, StandardOutPath=str(output), StandardErrorPath=str(errors))
    path.write_bytes(plistlib.dumps(config))
    try:
        run('launchctl', 'bootstrap', f'gui/{os.getuid()}', str(path))
        for _ in range(15):
            if 'Runtime access verified.' in output.read_text():
                return
            if errors.read_text():
                raise RuntimeError('macOS denied runtime access. Allow CLIProxyAPI Service in System Settings > Privacy & Security > Files and Folders.')
            time.sleep(1)
        raise RuntimeError('Launcher access check did not complete; inspect Library/Logs/CLIProxyAPI.')
    finally:
        run('launchctl', 'bootout', f'gui/{os.getuid()}/{label}', capture=True, check=False)
        path.unlink(missing_ok=True)


def restart(runtime):
    if not LAUNCHER.is_file():
        raise RuntimeError('Install the launcher with scripts/install-macos-launcher.sh first.')
    domain = f'gui/{os.getuid()}'
    agents_dir = Path.home() / 'Library/LaunchAgents'
    agents_dir.mkdir(parents=True, exist_ok=True)
    LOGS.mkdir(parents=True, mode=0o700, exist_ok=True)
    path = agents_dir / (LABEL + '.plist')
    content = plist(runtime)
    loaded = run('launchctl', 'print', f'{domain}/{LABEL}', capture=True, check=False).returncode == 0
    if loaded and path.exists() and path.read_bytes() == content:
        run('launchctl', 'kickstart', '-k', f'{domain}/{LABEL}')
    else:
        path.write_bytes(content)
        run('plutil', '-lint', str(path))
        run('launchctl', 'bootout', f'{domain}/{LABEL}', capture=True, check=False)
        run('launchctl', 'bootstrap', domain, str(path))


def health():
    with HTTP.open('http://127.0.0.1:8317/healthz') as response:
        return json.load(response)


def wait_health(info, attempts=45):
    last = 'no response'
    for attempt in range(attempts):
        try:
            reading = health()
            if reading.get('status') == 'ok' and all(reading.get(key) == info[key]
                   for key in ('commit', 'version')):
                return reading
            last = 'running build does not match the release'
        except (OSError, ValueError, urllib.error.URLError) as error:
            last = str(error)
        if attempt + 1 < attempts:
            time.sleep(1)
    raise RuntimeError(f'Deploy health failed: {last}')


def write_shortcuts(runtime):
    target = shlex.quote(f'gui/{os.getuid()}/{LABEL}')
    path = shlex.quote(str(Path.home() / 'Library/LaunchAgents' / (LABEL + '.plist')))
    commands = {
        'Iniciar.command': f'''if ! /bin/launchctl print {target} >/dev/null 2>&1; then
  /bin/launchctl bootstrap gui/$(id -u) {path}
fi
/bin/launchctl kickstart {target}
''',
        'Parar.command': f'/bin/launchctl bootout {target}\n',
    }
    for name, command in commands.items():
        shortcut = runtime / name
        backup = runtime / (name + '.before-launchd')
        if shortcut.exists() and not backup.exists():
            shutil.copy2(shortcut, backup)
        shortcut.write_text('#!/bin/sh\nset -eu\n' + command)
        shortcut.chmod(0o700)


def activate(runtime, release):
    info = check_release(release)
    current = runtime / 'current'
    previous = current.resolve() if current.is_symlink() else None
    atomic_link(current, release)
    try:
        restart(runtime)
        reading = wait_health(info)
        with HTTP.open('http://127.0.0.1:8317/management.html') as response:
            if hashlib.sha256(response.read()).hexdigest() != info['sha256']['management.html']:
                raise RuntimeError('Served management panel differs from the release.')
    except Exception:
        if previous is not None:
            atomic_link(current, previous)
            restart(runtime)
            wait_health(check_release(previous))
            print('Previous release restored.', file=sys.stderr)
        else:
            run('launchctl', 'bootout', f'gui/{os.getuid()}/{LABEL}', capture=True, check=False)
            current.unlink(missing_ok=True)
        raise
    if previous is not None and previous != release:
        atomic_link(runtime / 'previous', previous)
    write_shortcuts(runtime)
    print(json.dumps({'release': str(release), 'health': reading}, indent=2))


def validate_runtime(runtime):
    if not runtime.is_dir() or not (runtime / 'config.yaml').is_file():
        raise RuntimeError(f'Existing installation required: {runtime / "config.yaml"}')
    if not re.search(r'^\s*disable-auto-update-panel:\s*true\s*(?:#.*)?$',
                     (runtime / 'config.yaml').read_text(), re.MULTILINE):
        raise RuntimeError('Set management.disable-auto-update-panel: true before deployment.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['deploy', 'check-access', 'status', 'start', 'stop', 'uninstall', 'rollback'])
    parser.add_argument('--runtime', type=Path, default=RUNTIME)
    parser.add_argument('--build-root', type=Path, default=BUILD_ROOT)
    args = parser.parse_args()
    if sys.platform != 'darwin':
        raise RuntimeError('This command requires macOS.')
    runtime = args.runtime.resolve()
    validate_runtime(runtime)
    if args.command == 'status':
        result = run('launchctl', 'print', f'gui/{os.getuid()}/{LABEL}', capture=True)
        print(LABEL)
        print('\n'.join(line.strip() for line in result.stdout.splitlines()
                        if line.strip().startswith(('state =', 'pid =', 'last exit code ='))))
        print(json.dumps(health()))
        return
    with (runtime / '.deploy.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('Another service operation is running.') from None
        if args.command == 'check-access':
            check_access(runtime)
            print('Runtime access verified through launchd.')
            return
        if args.command == 'deploy':
            check_access(runtime)
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
            activate(runtime, release)
        elif args.command == 'rollback':
            previous = runtime / 'previous'
            if not previous.is_symlink():
                raise RuntimeError('No previous managed release is available.')
            activate(runtime, previous.resolve())
        elif args.command == 'start':
            activate(runtime, (runtime / 'current').resolve())
        else:
            run('launchctl', 'bootout', f'gui/{os.getuid()}/{LABEL}', capture=True, check=False)
            if args.command == 'uninstall':
                (Path.home() / 'Library/LaunchAgents' / (LABEL + '.plist')).unlink(missing_ok=True)
            print('Service stopped. Runtime data and releases are preserved.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
