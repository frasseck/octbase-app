You are a senior software architect performing the kickoff audit for Octbase's v0.1 production release (see `22_octbase-release-v01.md` for the overall mandate). This step produces the risk inventory that all later steps (`step_01`–`step_08`) will work from.

Do not fix anything in this step. Inspect, list, and prioritize only. Output a single audit report (markdown) that subsequent steps can reference.

## Practical steps

1. **Repo diff since last full audit**
   ```bash
   git log --oneline 25144a1..HEAD
   git diff --stat 25144a1..HEAD
   ```
   Summarize what changed: new endpoints, new migrations, new frontend modules, new dependencies.

2. **Open risk markers**
   ```bash
   grep -rniE 'TODO|FIXME|HACK|XXX|not implemented|for now|temporary' \
     octbase-api/internal octbase-frontend/js octbase-frontend/css docs/ README*.md
   ```
   For each hit, classify: Blocker / High / Medium / Low / Not relevant to release.

3. **CI reality check**
   - Read `.github/workflows/ci.yml`. Confirm lint/test/build actually run and gate `main`.
   - Note the placeholder `# Add your registry push commands here` — confirm whether a registry exists (check `.env`, `docs/operations.md`, any deploy scripts).

4. **Config drift**
   - Extract every `os.Getenv` / config struct field referenced in `octbase-api/internal/**` (likely centralized in a `shared` or `config` package):
     ```bash
     grep -rn 'os.Getenv\|Getenv(' octbase-api/internal | sort -u
     ```
   - Diff against `.env.example` and the tables in `README.md` and `docs/operations.md`. List any variable read by code but undocumented, or documented but unused.

5. **Data durability check**
   - Confirm whether `docs/operations.md`'s backup cron is implemented anywhere as a script/file in the repo, or exists only as documentation prose.
   - Check `migrations/` for `down` files for every `up` migration (001–latest):
     ```bash
     ls octbase-api/migrations/ | sort
     ```

6. **Single points of failure (read-only review)**
   - `internal/shared` (DB pool setup) — what happens on connection failure at startup vs. at runtime?
   - `internal/mailer` — confirm the stdout fallback path when `OCTBASE_SMTP_HOST` is empty.
   - `internal/sse` — look for goroutine leaks on client disconnect (missing `defer`/context cancellation).

7. **Demo-mode / bootstrap check**
   - Check `internal/seed` (or wherever `OCTBASE_DEMO_MODE` seeding happens). With `OCTBASE_DEMO_MODE=false`, is there ANY way to create the first SUPER_ADMIN user? Search migrations for a seeded admin row, and search `cmd/octbase-api/main.go` for first-run bootstrap logic. This is the single most important finding in this step — flag it loudly if missing.

## Output format

A markdown report with these sections, each finding tagged with severity and a pointer to the step that should fix it:

```
| # | Finding | Severity | File(s) | Owning step |
|---|---------|----------|---------|-------------|
| 1 | ... | Blocker | path:line | step_05 |
```

Plus:
- One-paragraph "since last audit" summary.
- A short "release blockers at a glance" list (just the Blocker-severity rows).

Save this report as `prompts/_release-v01-audit.md` so steps 01–08 can reference it. Do not modify any other files in this step.
