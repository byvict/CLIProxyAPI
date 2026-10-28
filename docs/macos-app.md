# Keep the local app available on macOS

Use the same delivery sequence as Fin Dash: integrate into the definitive `main`,
validate there, push, deploy, and confirm the running commit. This guide applies to
the local fork at `byvict/CLIProxyAPI`. Keep `origin` for upstream reads and push local
changes to `fork`.

## Integrate and version a change

1. Start from `fork/main`, which contains the local management panel and history features.
2. Update `VERSION` and `CHANGELOG.local.md` for each delivered batch. Increment the
   local number in `8.0.44-local.N`. The first three numbers identify the upstream baseline.
3. Run the applicable tests and commit the change on its work branch.
4. In `/Users/victorbaccega/CLIProxyAPI`, confirm that the working tree is clean.
5. Run `git fetch fork` and `git merge --ff-only fork/main` on `main`.
6. Integrate the work branch with `git merge --ff-only <branch>`. If it has diverged,
   reconcile it with `fork/main` in the worktree first. Preserve other work.
7. Verify that `git merge-base --is-ancestor <work-commit> main` succeeds.
8. Run `git push fork main` and deploy from the definitive checkout.

If a change uses a pull request, target the fork's `main` and integrate the merged
result before deployment. The upstream `dev` integration policy applies to the
upstream repository. Merge alone does not replace the local executables.

## Deploy the integrated version

On the current Mac, run:

```sh
cd /Users/victorbaccega/CLIProxyAPI
python3 -B scripts/macos-app.py deploy
```

The command uses the existing Go toolchain recorded in
`/Volumes/Arquivos/cliproxyapi-build/toolchain-bin-path.txt` and the installed Bun 1.4.3.
On another Mac, make Go, Bun 1.4.3, Git, and Python 3.9 or newer available in `PATH`.

The command requires a clean `main` that matches the pushed `fork/main`. It runs the
service deployment tests and `go test ./...`, checks Go formatting, builds both Go
programs with version and commit metadata, and builds the pinned management panel.
The panel build runs its tests, type checks, lint, and production build.
A source change during the build aborts deployment before activation.

The release contains both binaries, `management.html`, and a `release.json` with
SHA-256 checksums. Releases stay under
`/Volumes/Arquivos/cliproxyapi-original/releases/`. The `current` link selects the
release. Redeployment checks existing artifacts against their manifest. A different
commit requires a different `VERSION`.

Deployment restarts both LaunchAgents and waits for both `/healthz` endpoints to
return the release's version and full commit. It also checks the served panel's
SHA-256. A failed activation restores the previous release and checks its health.
Concurrent service operations are rejected by a filesystem lock.

Keep `management.disable-auto-update-panel: true` in the existing configuration.
The proxy serves the panel from the selected release through `MANAGEMENT_STATIC_PATH`.
Deploy does not rewrite configuration, auth files, access keys, or history databases.
It uses the existing installations and stops if their directories are missing.

## Replace the manual installation once

1. Build and validate the release before stopping the installed processes.
2. Back up the existing startup shortcuts and record both process IDs and executable paths.
3. Stop the two legacy tmux sessions with their existing `Parar.command` shortcuts.
4. Deploy the prepared release. On a first-install failure, the command unloads both
   new agents. Use the preserved legacy startup commands to restore service.
5. Run the status command below and compare the served panel with the built artifact.
6. Send `SIGTERM` to each agent once and confirm that `launchd` creates new PIDs and
   that both health endpoints still identify the expected commit.

The successful first deploy replaces `Iniciar.command` and `Parar.command` in each
installation with launchd commands. Their first versions remain in `.before-launchd`
files. Existing `Abrir-painel.command` shortcuts keep working.

## Check or control the services

```sh
python3 -B scripts/macos-app.py status
python3 -B scripts/macos-app.py rollback
python3 -B scripts/macos-app.py stop
python3 -B scripts/macos-app.py start
python3 -B scripts/macos-app.py uninstall
```

`stop` unloads both jobs for the current login. `start` loads them again and checks
release health. `uninstall` also removes their plists and preserves runtime data.
`rollback` switches to the last managed release. The original tmux binaries remain
available for first-install recovery.

Open the panels at:

- Proxy: <http://127.0.0.1:8317/management.html>
- V1 monitor: <http://127.0.0.1:8318>

The LaunchAgents are `com.byvict.cliproxyapi` and `com.byvict.cliproxyapi.monitor` in
`~/Library/LaunchAgents/`. Both use `RunAtLoad` and `KeepAlive`. Logs are
`logs/launchd.stdout.log` and `logs/launchd.stderr.log` in each existing installation.

Keep the Mac powered on and awake, the SSD mounted, and the operational user logged
in. Locking the screen is supported. After a restart, unlock FileVault and log in;
launchd then starts the services without opening a terminal. A reboot is a separate
acceptance check and must be reported as unverified until performed.

The V1 monitor's existing background collection continues while its panel is closed.
The main panel's quota collection still requires an open panel. This installation
does not change collection schedules, session lifetimes, or provider authentication.
