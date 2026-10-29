# Keep the main proxy available on macOS

Use the Fin Dash delivery sequence: integrate into the definitive `main`, validate
there, push, deploy, and confirm the running commit. This guide applies to
`byvict/CLIProxyAPI` and the main panel on port 8317. The separate V1 monitor on
port 8318 stays outside these service and deployment commands.

## Integrate and version a change

1. Start from `fork/main`, which contains the local panel and history features.
2. Update `VERSION` and `CHANGELOG.local.md` for each delivered batch. Increment
   the local number in `8.0.44-local.N`. The first numbers identify the upstream baseline.
3. Run the applicable tests and commit the change on its work branch.
4. In `/Users/victorbaccega/CLIProxyAPI`, confirm that the working tree is clean.
5. Run `git fetch fork` and `git merge --ff-only fork/main` on `main`.
6. Integrate the work branch with `git merge --ff-only <branch>`. If it has diverged,
   reconcile it with `fork/main` in the worktree first. Preserve other work.
7. Verify that `git merge-base --is-ancestor <work-commit> main` succeeds.
8. Run `git push fork main` and deploy from the definitive checkout.

If a change uses a pull request, target the fork's `main` and integrate the merged
result before deployment. Push local changes to `fork`; keep `origin` for upstream
reads. The upstream `dev` policy applies to the upstream repository.

## Runtime location

The runtime is `~/Dev/CLIProxyAPI/app`, with configuration, credentials, databases, and
releases. Deployment builds in `~/Dev/CLIProxyAPI/build`.

The delegation observatory keeps its data, control, and publication directories on the
SSD under `/Volumes/Arquivos/cliproxyapi-original`. The runtime's `delegation-observatory`
entry is a symbolic link to the published directory there. Without the SSD, only the
delegation page is unavailable.

Install the stable launcher with Go in `PATH`:

```sh
bash scripts/install-macos-launcher.sh
open -n "$HOME/Applications/CLIProxyAPI Service.app" --args --check "$HOME/Dev/CLIProxyAPI/app"
```

The launcher does not require Full Disk Access.

Verify access through launchd before stopping the manual installation:

```sh
python3 -B scripts/macos-app.py check-access
```

Expect `Runtime access verified through launchd.` This command leaves the running
proxy untouched. Deployment repeats this check before activation.

The launcher lives in `~/Applications/CLIProxyAPI Service.app`. It starts the selected
proxy binary in the runtime and forwards termination signals. Normal deployments retain
this launcher and its macOS privacy identity. The installer leaves an existing launcher
in place. If its source needs an update, replace it explicitly and authorize it again.

## Deploy the integrated version

```sh
cd /Users/victorbaccega/CLIProxyAPI
python3 -B scripts/macos-app.py deploy
```

The command uses the existing Go toolchain recorded in
`~/Dev/CLIProxyAPI/build/toolchain-bin-path.txt` and the installed Bun 1.4.3.
On another Mac, make Go, Bun 1.4.3, Git, Python 3.9 or newer, and `zstd` available in `PATH`.

Deployment requires a clean `main` matching the pushed `fork/main`. It runs the
`scripts/test_*.py` tests, including the history importer's, and `go test ./...`,
checks Go formatting, builds the proxy with version and commit metadata, and builds
the pinned management panel. The panel build runs its tests, type checks, lint, and
production build. A source change during the build aborts activation.

Each release contains `cli-proxy-api`, `management.html`,
`import-performance-history.py`, and a `release.json` with SHA-256 checksums under
`~/Dev/CLIProxyAPI/app/releases/`. The `current` link selects the
release. Redeployment checks artifacts against their manifest, which lists only that
release's files, so releases built before the importer became an artifact remain valid
for rollback. A different commit requires a different `VERSION`.

Deployment restarts `com.byvict.cliproxyapi`, waits for `/healthz` on port 8317 to
return the release's version and full commit, and verifies the served panel's SHA-256.
A failed activation restores the previous managed release and checks its health.
Concurrent service operations are rejected by a filesystem lock.

Keep `management.disable-auto-update-panel: true` in the existing configuration.
The launcher sets `MANAGEMENT_STATIC_PATH` to the selected release's panel.
Deployment preserves configuration, credentials, and history databases.

For the first installation, prepare and validate the release before stopping the
legacy proxy with its existing `Parar.command`. Deploy the prepared release and
check its health. If this first activation fails, use the preserved legacy startup
command to restore service. Leave the monitor on port 8318 running as before.

A successful deploy replaces the proxy's `Iniciar.command` and `Parar.command` with
launchd commands. Their first versions remain in `.before-launchd` files.
`Abrir-painel.command` keeps working.

## Check or control the service

```sh
python3 -B scripts/macos-app.py status
python3 -B scripts/macos-app.py rollback
python3 -B scripts/macos-app.py stop
python3 -B scripts/macos-app.py start
python3 -B scripts/macos-app.py uninstall
```

`stop` unloads the proxy and history import jobs for the current login. `start` loads
them again and checks release health. `uninstall` also removes both plists and preserves
runtime data. `rollback` switches to the previous managed release. `status` prints the
state and last exit code of both jobs.

Open <http://127.0.0.1:8317/management.html>. The LaunchAgent plist is
`~/Library/LaunchAgents/com.byvict.cliproxyapi.plist`. It uses `RunAtLoad` and `KeepAlive`.
Logs are `~/Library/Logs/CLIProxyAPI/launchd.stdout.log` and `launchd.stderr.log`.

After installation, send `SIGTERM` to the LaunchAgent once. Confirm that launchd
creates a new PID and that health still reports the expected commit.

Keep the Mac powered on and awake and the user logged in. The delegation page also
needs the SSD mounted. Locking the screen is supported. After a restart, unlock
FileVault and log in; launchd then starts the service. A reboot is a separate
acceptance check and stays unverified until performed.

The main panel's quota collection still requires an open panel. This installation
does not change collection schedules, session lifetimes, or provider authentication.

## Performance history import

The LaunchAgent `com.byvict.cliproxyapi.performance-import` keeps
`performance-history.sqlite` current for the Performance page. It runs
`/usr/bin/python3 -B <runtime>/current/import-performance-history.py` at load and
every 15 minutes, so a deploy or rollback also switches the importer. launchd does not
start a run while the previous one is still running. The job's `PATH` includes
`/opt/homebrew/bin` for `zstd`.

Deployment installs the job after the proxy passes its health and panel checks. Its
plist is `~/Library/LaunchAgents/com.byvict.cliproxyapi.performance-import.plist`.
`Parar.command` stops only the proxy; scheduled imports continue and only add rows.

Each run writes its receipt to `~/Library/Logs/CLIProxyAPI/performance-import.json`,
with new token events and coverage per provider. Errors go to
`performance-import.stderr.log` in the same directory. A failed run commits no rows,
and the next interval retries. After a rollback to a release without the importer, the
job fails on the missing file and changes nothing.

The history uses SQLite WAL mode, so the proxy keeps reading committed rows while an
import writes. A manual run of the same command waits up to 5 seconds for a running
import, then fails with `database is locked` and changes nothing. Before the first
import of each São Paulo day, the importer copies the history to
`performance-history.before-YYYYmmdd.sqlite` and keeps the newest three dated copies.
Copies named `.before-YYYYmmddHHMMSS.sqlite` from earlier manual runs are not touched.
