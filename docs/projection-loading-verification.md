# Verify initial quota projections

The quota timeline can use recorded observations before the first provider reply.
These readings retain their timestamps and forecast freshness rules. They never
populate provider stores or create new persisted observations. A successful live
response, including one with no readable windows, takes precedence.

Build the management artifact with `scripts/build-management.sh`, then run:

```sh
PLAYWRIGHT_MODULE=/absolute/node_modules/playwright/index.mjs \
PLAYWRIGHT_CHROMIUM_EXECUTABLE=/absolute/chrome \
TMPDIR=/absolute/ssd-run/tmp \
node scripts/projection-loading-ui-check.mjs /absolute/management-build /absolute/ssd-run/ui
```

The checker intercepts every request before login. It uses synthetic credentials
and observations. It saves a screenshot, visible text, and a JSON receipt. No
running proxy or real management key is needed.

The checks require four recorded lanes and provisional forecasts while all
provider requests are held. They also cover immediate remount, failed history
refresh, stale readings, replacement by live quota, authoritative unknown windows,
logout, and connection changes. Empty and unknown histories must not invent lanes.

Run the existing `data-loading-ui-check.mjs`, `compact-quota-ui-check.mjs`, and
`quota-auto-refresh-ui-check.mjs` against the same production bundle.

## Verify authentication

```sh
go test -race ./internal/api/handlers/management -run 'TestManagementAuth|TestAuthenticateManagementKey|TestMiddleware'
for run in 1 2 3 4 5; do
  go test ./internal/api/handlers/management -run '^$' \
    -bench '^BenchmarkManagementAuthHTTP$' -benchtime=12x -count=1
done
```

Set `TMPDIR`, `GOCACHE`, and `GOMODCACHE` to SSD paths before these commands.
The HTTP benchmark uses the real management middleware and configuration handler,
a synthetic bcrypt password at `bcrypt.DefaultCost`, and checks every status and
response body. `Baseline` clears the single success slot before each request to
reproduce the old bcrypt path. `Warm` primes it once. `Cold` clears the slot and
private entropy before each request. These are test-only operations.

The named `AUTH-WARM-CPU` gate requires a separate real HTTP CPU profile with
at least a 90% reduction in the cumulative share of `blowfish.ExpandKey` samples
for warm valid requests relative to `Baseline`. Behavior tests alone cannot prove
that a repeated success skipped bcrypt. Use `-cpuprofile` with the benchmark and
`-o /absolute/ssd-run/management.test`, then inspect `go tool pprof -top`.

Capture CPU profiles in separate benchmark runs. Go's benchmark calibration and
setup also appear in profiles, so warm profiles still contain initial password
hashing and first verification. Do not describe those samples as warm requests.

The benchmark isolates authentication and configuration reads. It does not measure
installed HTML delivery, host contention, or provider latency. Installed first
opening and reopening still require a separate authenticated browser measurement.
