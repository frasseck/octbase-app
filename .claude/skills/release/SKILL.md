---
name: release
description: Release a release_vN branch of Octbase — changelog release entry, version stamping via OCTBASE_APP_VERSION, asset cache-busting, merge to main via scripts/release.sh; the demo deploy then runs via octbase-service from the admin machine. Use when asked to release, cut a version, ship to the demo, or deploy.
---

# Releasing Octbase

Work happens on `release_vN` branches; a release merges the branch into `main`
and redeploys the live demo install. The pieces below are easy to miss — walk
them in order.

> **Deploys ship released code only** (Lars, 2026-07-27): client instances
> (beyags, demo, any `oct-*`) run tagged `vX.Y.Z` builds from `main` — never an
> unmerged `release_vN` branch. Unreleased schema 034 reached beyags/demo once;
> that was an accident, not policy (see `docs/operations.md` § Deploy).

## 1. Pre-release checks

- Tests green (`testing` skill) and coverage at/above the floor (`coverage` skill).
- Frontend CI guards clean (`frontend-guards` skill) — shared-module drift and
  stale asset hashes are the two that bite at release time.

## 2. Changelog release entry

`CHANGELOG.md` accumulates entries under `## Unreleased` as changes land. At
release time:

1. Rename `## Unreleased` to `## vX.Y.Z — YYYY-MM-DD` (match the existing
   entries, e.g. `## v1.0.1 — 2026-07-02`).
2. Open a fresh, empty `## Unreleased` heading above it.

**Choosing X.Y.Z: default to a patch bump (`Z += 1`).** A minor bump
(`Y += 1, Z = 0`) is a **manual, explicit decision** — a human must say so for
this specific release (content alone doesn't decide it: a release that adds
new features is not automatically minor, see the v1.0.3 precedent). Absent
that explicit instruction, bump patch regardless of how much landed. Never
infer or default to a minor bump.

## 3. Version stamping — **do not** touch `defaultAppVersion`

The build default (`defaultAppVersion` in `octbase-api/cmd/octbase-api/main.go`)
is deliberately `beta` and is **never bumped per release**. A release stamps the
version in the **deployment's `.env`** instead:

```
OCTBASE_APP_VERSION=X.Y.Z
```

(see `.env.example`). Each install has its own `.env`, so set it in the install
being deployed. For the platform-managed demo (oct-demo account, see step 5)
the stamp lives in `octbase-service/ledger/clients/demo.yml` (`app_version:`)
and is applied by `create-instance.yml` from the admin machine. The value
surfaces at `/health`, `/api/v1/version`, `/api/v1/config`, and the frontend's
bottom-center `octbase X.Y` tag reads it from `/config` at boot.

**Version-number baseline:** platform syncs may stamp deployment versions
without a changelog release (1.0.5/1.0.6 were such stamps). Check the live
demo (`curl -s https://demo.ocete.ch/health`) and bump patch from the highest
deployed/stamped version, not just from the changelog's last heading.

## 4. Asset cache-busting — nothing to do (since 37b stage 5)

The Vite build content-hashes every asset filename, including the classic
scripts outside the module graph, so a release can neither carry a stale hash
nor need a restamping step. The old `scripts/stamp-assets.py` pass and its
`--check` CI guard are gone; `octbase-frontend/index.html` no longer contains
anything derived.

The images are built from the repo root and run the build themselves, so
whatever is committed is what gets fingerprinted.

## 5. Merge: `scripts/release.sh`

```bash
scripts/release.sh -m "commit message" --yes   # commit dirty tree + release
scripts/release.sh --yes                        # tree must already be clean
```

It: commits any working-tree changes (**`git add -A`** — check `git status`
first so you don't ship strays), pushes the branch, merges `--no-ff` into
`main`, pushes `main`, and returns to your branch. Without `--yes` it prompts
interactively — always pass `--yes` when driving it from an agent.

## 6. Demo deploy — platform-managed, runs from the ADMIN machine

Since 2026-07-13 the public demo runs under its own `oct-demo` account,
provisioned by `octbase-service` (see its `migrate-instance` /`client-ops`
skills). There is **no local demo checkout** on this host anymore
(`/home/claude/demo.ocete.ch` is archived), and Ansible is not installed
here — `release.sh` skips its legacy deploy step with a notice.

From the **admin machine**, in `octbase-service`:

```bash
# stamp the released version first (ledger app_version: "X.Y.Z"), then:
ansible-playbook playbooks/create-instance.yml -e client=demo   # applies .env incl. version
ansible-playbook playbooks/sync-instance.yml -e client=demo     # sync main, rebuild, restart
```

## 7. Post-deploy gate

Confirm health via the public URL before standing down:

```bash
curl -s https://demo.ocete.ch/health   # expect status ok + the released version
```

## Related

- Tests → `testing` · Coverage floor → `coverage` · CI guards → `frontend-guards`
- Stack diagnosis → `stack-health` · Stacks/ports/checkouts → `dev-stack`
