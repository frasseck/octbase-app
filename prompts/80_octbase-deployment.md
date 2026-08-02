> **Correction (2026-07-27): Mailpit is NOT part of the deployable stack.**
> Since 2026-07-02 the base `podman-compose.yml` is postgres + api + frontend +
> mobile only (4 services); Mailpit exists solely in the dev overlay
> `podman-compose.dev.yml` and must never be deployed. Ignore every Mailpit
> reference below (`MAILPIT_UI_AUTH`, the mailpit resources row) — production
> mail goes out via the `smtp:` settings, and unset SMTP falls back to stdout.

You are a senior platform / infrastructure engineer. Your job is to make Octbase
**deployable as one isolated per-client instance at a time**, from a single
Ansible run driven entirely by a variables file. This is the technical execution
of the multi-tenant model already designed in
[`docs/hosting-concept.md`](../docs/hosting-concept.md) (read it first — especially
§2 architecture, §7 per-service resource limits, §8 network/TLS/routing) and
priced in [`docs/business-plan.md`](../docs/business-plan.md).

Ground truth before you start: the reference stack is `podman-compose.yml`
(postgres + octbase-api + octbase-frontend + octbase-mobile; **no Mailpit** — see the correction above), it already
reads per-instance settings from env (`POSTGRES_*`, `API_PORT`, `FRONTEND_PORT`,
`PGDATA_DIR`, `OCTBASE_JWT_SECRET`, `OCTBASE_SCM_ENC_KEY`, `OCTBASE_CORS_ORIGIN`,
`OCTBASE_APP_URL`, `MAILPIT_UI_AUTH`, the `OCTBASE_DB_*` pool knobs) and already
declares `deploy.resources` limits per service. The frontend Caddy container is
the **per-instance front door** (`octbase-frontend/caddy/Caddyfile` /
`Caddyfile.tls`); it is *not* the machine's edge. Do not duplicate what exists —
wire it up.

## The model to implement

One Linux user per client, one full `podman-compose` stack per client, all
rootless, all on one host until §6 of the hosting concept says to scale out. A
**single machine-level "main Caddy" edge** terminates TLS for every client's DNS
name and reverse-proxies to that client's stack. Everything a single instance
needs — its DNS name, ports, secrets, Postgres credentials, and the CPU/memory
Podman may use — comes from **one per-instance variables file**; no value is
hard-coded in a play.

## Deliverable: an Ansible project under `deploy/ansible/`

```
deploy/ansible/
├── README.md                    # how to add a client, run, and tear down
├── ansible.cfg
├── inventory/
│   └── hosts.yml                # the host(s) that run instances
├── group_vars/
│   └── all.yml                  # platform-wide defaults (base domain, edge Caddy paths, image registry)
├── host_vars/                   # (optional) per-host overrides
├── clients/
│   ├── _example.yml             # the documented variables template (every knob, commented)
│   └── acme.yml                 # a real per-instance file (one per client)
├── group_vars/all/vault.yml     # ansible-vault: shared secrets (registry creds, ACME email)
├── site.yml                     # entry play: provision/enable an instance
├── deprovision.yml              # tear an instance down safely (keep data by default)
└── roles/
    ├── host_base/               # rootless podman prerequisites on the host
    ├── main_caddy/              # the shared machine edge, config assembled from per-instance snippets
    └── octbase_instance/        # everything that belongs to one client user
```

## Practical steps

1. **Per-instance variables file is the contract.** `clients/_example.yml` defines
   and documents every variable a single instance takes. At minimum:
   ```yaml
   client_id: acme                     # short slug -> linux user "octbase-acme", stack project name
   dns_name: acme.octbase.app          # the instance's own DNS name (its Caddy vhost)
   admin_email: ops@acme.example       # first SUPER_ADMIN / license contact
   # Podman resources this instance may use (fed straight into deploy.resources
   # limits/reservations of the generated compose file — see hosting-concept §7):
   resources:
     api:      { cpus: "1.0", memory: "256M", reservations: { cpus: "0.10", memory: "64M" } }
     postgres: { cpus: "2.0", memory: "1024M", reservations: { cpus: "0.25", memory: "256M" } }
     frontend: { cpus: "0.5", memory: "64M" }
     mobile:   { cpus: "0.5", memory: "64M" }
     mailpit:  { cpus: "0.25", memory: "128M" }
   octbase_image_tag: v0.1.0           # pin per instance so upgrades are per-client
   smtp: { host: "", port: 587, from: "noreply@acme.example", user: "", pass: "" }
   # secrets: NEVER in this file. Generated or pulled from vault (see step 4).
   ```
   Validate required vars with `assert` at the top of the role; fail early with a
   clear message when `dns_name`/`client_id` is missing or malformed
   (`client_id` must be `^[a-z][a-z0-9-]{1,30}$` — it becomes a username, a
   directory, and a compose project name).

2. **`host_base` role — rootless Podman prerequisites (idempotent).**
   - Ensure `podman`, `podman-compose` (or the `podman compose` plugin) and
     `caddy` are installed.
   - Create the per-client system user `octbase-{{ client_id }}` with its own home,
     `loginctl enable-linger` so its rootless containers survive logout/reboot, and
     subuid/subgid ranges allocated (one non-overlapping block per client).
   - Enable `podman-restart.service` for that user (the compose stack sets
     `restart: always` — see the note in `octbase-api/README.md` "Start on boot").

3. **`octbase_instance` role — one client's stack.** All actions run *as the
   client user* (`become_user`). 
   - Lay down the stack under `~/stack/`: the pinned images (pulled from the
     registry, or built), a generated **`.env`** rendered from the client vars +
     generated secrets, and a generated **`podman-compose.yml`** (template the
     reference file — do **not** publish `POSTGRES_PORT`/`API_PORT` on `0.0.0.0`;
     bind them to `127.0.0.1` or a per-user internal network, since the main Caddy
     is the only public entry point). Set production values:
     `OCTBASE_DEMO_MODE=false`, `OCTBASE_SECURE_COOKIES=true`,
     `OCTBASE_CORS_ORIGIN=https://{{ dns_name }}`, `OCTBASE_APP_URL=https://{{ dns_name }}`,
     `MAILPIT_UI_AUTH` set (never blank in prod).
   - **Resources:** render each service's `deploy.resources` block directly from the
     `resources:` map in the client file. This is the "make the resources Podman can
     use configurable per instance" requirement — one place, per client.
   - Assign this instance a unique host loopback port pair (derive deterministically
     from `client_id`, or track allocations in `group_vars`) so the edge can reach it.
   - Bring the stack up (`podman-compose -p octbase-{{ client_id }} up -d`), then
     **gate on health** before declaring success by running
     `octbase-operations/check-health.sh --project octbase-{{ client_id }} --quiet`
     in a retry loop (reuse it, don't reinvent it).

4. **Secrets — generated once, stored, never re-rolled on re-run.**
   - `OCTBASE_JWT_SECRET` (≥32 bytes), `OCTBASE_SCM_ENC_KEY` (32-byte AES key,
     base64), and the Postgres password are generated on first provision and
     persisted (ansible-vault file per client, or the host user's `~/secrets/`
     mode `0600`). Re-running the play must **reuse** them — regenerating
     `OCTBASE_JWT_SECRET` logs every user out, and rotating `OCTBASE_SCM_ENC_KEY`
     makes stored SCM tokens undecryptable. Make this property explicit and test it.

5. **`main_caddy` role — enhance the shared edge, don't replace it.** The machine
   has one system-level Caddy. Its `Caddyfile` is assembled from per-instance
   snippets so adding a client is additive and idempotent:
   ```
   # /etc/caddy/Caddyfile
   import /etc/caddy/sites-enabled/*.caddy
   ```
   The role drops `/etc/caddy/sites-enabled/{{ client_id }}.caddy`:
   ```
   {{ dns_name }} {
       reverse_proxy 127.0.0.1:{{ instance_frontend_port }} {
           flush_interval -1   # keep SSE working end-to-end (see the Caddyfile note)
       }
   }
   ```
   Automatic HTTPS gives each `dns_name` its own certificate. `validate` the config
   (`caddy validate --config /etc/caddy/Caddyfile`) and reload (never restart —
   `systemctl reload caddy`) so adding one client never drops the others. Keep the
   security headers / CSP that the per-instance `Caddyfile.tls` already sets, or set
   them at the edge — decide one owner for headers and document it.

6. **Idempotency & re-runnability.** Running `site.yml` for an existing client must
   converge with no destructive changes: same secrets, same data volume, image tag
   updated only if the client var changed, edge reloaded only on a real diff. Support
   `--limit` / a `client=` extra-var so an operator provisions or updates **one**
   client without touching the rest. Confirm with `--check --diff`.

7. **Deprovision (`deprovision.yml`).** Remove the edge snippet + reload, stop and
   remove the stack, and **by default keep the data volume and a final DB dump**
   (`pg_dump` to `~/backups/`), deleting the Linux user/data only behind an explicit
   `confirm_destroy=true`. Deleting a client's Postgres volume destroys all their
   data — make that a two-step, opt-in action, mirroring the warning in
   `octbase-operations/README.md`.

8. **Upgrades.** Document the per-client rolling upgrade: bump `octbase_image_tag`
   in the client file, re-run with `client=acme`. Migrations run automatically at
   API startup (golang-migrate); health-gate the result and note the documented
   rollback (`migrate down 1`) from `docs/operations.md`.

## Cross-references / do-not-break
- The order/provisioning control plane (see `82_octbase-order.md`) is the **caller**
  of this playbook: an approved order writes a `clients/<slug>.yml` and runs `site.yml`
  limited to that client. Keep the variables-file interface stable and machine-writable.
- Monitoring (see `81_octbase-operation.md`) discovers instances by the same
  `client_id` / compose-project convention — emit an inventory artifact
  (e.g. `deploy/ansible/clients/*.yml` is the source of truth for "which instances exist").

## Deliverable summary (write `deploy/ansible/README.md`)
- The exact "add a new client" runbook (create `clients/<slug>.yml` → run → verify).
- The variables reference (mirror `_example.yml`).
- The resource-tuning guidance, cross-linked to `hosting-concept.md` §7.
- The upgrade and deprovision procedures.

## Verification
```bash
cd deploy/ansible
ansible-lint .
ansible-playbook site.yml --syntax-check
# Dry run one client end to end:
ansible-playbook site.yml -e client=acme --check --diff
# Real run against a test host, then prove health + isolation:
ansible-playbook site.yml -e client=acme
../../octbase-operations/check-health.sh --project octbase-acme
curl -sS -o /dev/null -w '%{http_code}\n' https://acme.octbase.app/health   # 200 via the edge
# Re-run must be a no-op (idempotent) and must NOT rotate secrets:
ansible-playbook site.yml -e client=acme --check --diff   # expect no changes
```
Provision a **second** client on the same host and confirm the two stacks are
isolated (separate users, volumes, ports, certs) and that neither `check-health.sh`
project nor the edge for one client is affected by touching the other.
