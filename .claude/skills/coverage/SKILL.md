---
name: coverage
description: Check Go backend test coverage against the CI floor for octbase-api before pushing. CI enforces a hard coverage floor (currently 73.0%) that must never be lowered to make a red build pass. Use when adding/changing backend code or before opening a PR, to confirm coverage won't regress.
---

# Coverage floor (octbase-api)

CI (`.github/workflows/ci.yml`, `test` job) measures statement coverage and
**fails the build if total coverage drops below `MIN` (currently `73.0`%)**.

> The floor is a one-way ratchet: raise `MIN` as coverage improves; **never
> lower it to turn a red build green.** If you add code, add tests for it.

## Reproduce the CI check locally

Coverage needs a real Postgres via `TEST_DATABASE_URL` (tests that need a DB
skip without it — and skipped tests count as uncovered). Point it at a running
stack's Postgres (see the `dev-stack` skill) on its own database:

```bash
cd /home/claude/dev.octbase.io/octbase-api
export TEST_DATABASE_URL="postgres://octbase:octbase@localhost:5432/octbase_test?sslmode=disable"

go test ./... -count=1 -coverprofile=cover.out
go tool cover -func=cover.out | tee coverage.txt
grep -E '^total:' coverage.txt          # the number CI compares to the floor
```

A quick pass/fail mirroring CI:

```bash
total=$(grep -E '^total:' coverage.txt | awk '{print $3}' | tr -d '%')
awk -v t="$total" -v m="73.0" 'BEGIN { if (t+0 < m+0) print "BELOW FLOOR"; else print "OK" }'
```

## Notes

- Some packages are effectively untestable by design (the `cmd` main wiring,
  `internal/seed` demo data, `internal/testutil`). Don't chase their lines;
  focus new tests on the handler/service/repo packages.
- Handler tests are integration-style via `internal/testutil` (real chi router +
  real migrations against Postgres) — follow that pattern rather than mocking.
- `go tool cover -html=cover.out -o cover.html` to see exactly which lines are
  uncovered.

## Related

- Running the suite → `testing` skill
- Getting a Postgres → `dev-stack` skill
