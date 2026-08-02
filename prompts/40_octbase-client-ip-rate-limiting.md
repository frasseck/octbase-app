# Octbase — restore per-client rate limiting (recover individual client IPs)

> **STATUS: DONE (landed 2026-07-16; verified in code 2026-07-27).** The full
> chain is in place: `FRONTEND_BIND_ADDR` port binding in `podman-compose.yml`,
> `trusted_proxies static` in **both** Caddyfiles, `OCTBASE_TRUSTED_PROXIES` /
> `OCTBASE_FRONTEND_TRUSTED_PROXIES` documented in `.env.example`, and
> `TestRealIP_DeployedTopology` (incl. the spoofing negative) in
> `internal/shared/realip_test.go`. The topology claims below (":8081 on all
> interfaces", host-Caddy details) describe the **pre-fix** state — this prompt
> is historical; do not re-execute or "fix" the settled trust boundary from it.

You are a senior platform + application-security engineer. Octbase's per-IP rate
limiting is currently **effectively global**: every client in an installation
shares one bucket, because no individual client IP survives the path from the
browser to the API. Ordinary login traffic can therefore 429 real users, and the
auth budget (120/min) is a budget for *all users combined* rather than per user.

This prompt fixes that. It is the open half of a two-part problem; **the other
half is already done — do not re-do it:**

- **DONE (commit `bdc6ea6`, 2026-07-15):** `shared.RateLimit` handed every call
  site one package-level counter, so the auth (120/min) and user-management
  (60/min) budgets collided. Each call now owns an independent counter, covered
  by `octbase-api/internal/shared/ratelimit_test.go`. That fix is correct under
  any topology and is unaffected by this work.
- **OPEN (this prompt):** the client IP itself is destroyed upstream of the API,
  so all clients key into the same bucket.

Follow the repo's normal change discipline (`CLAUDE.md`): a test or a concrete
verification for anything with runtime behavior, run `go test ./...` + the
coverage floor (`coverage` skill), keep `CHANGELOG.md` (`## Unreleased`
`### Security`) and the docs in sync (mandatory-change-checks), and never weaken
a CI guard or lower the coverage floor. Use the `go-security` skill on any change
to the trust logic. **Do not touch a real client deployment** — `oct-demo` and
any client stack are off-limits; verify on `octbase_dev` or a disposable stack
(`dev-stack` skill).

---

## The verified evidence (2026-07-15) — don't re-derive it

Topology of the dev host, confirmed by inspection:

```
internet
  → host Caddy  :80/:443   (root, /etc/caddy/Caddyfile, pid 1535)
      dev.ocete.ch { reverse_proxy 178.105.142.1:8081 }   # NB: public IP, not loopback
  → rootlessport (rootlesskit port handler; podman 5.7.0 rootless, netavark)
  → frontend Caddy container   10.89.4.5   (octbase-frontend, hi/caddy)
  → octbase-api:8000           10.89.4.3
```

The failure chain, each step observed on the running `octbase_dev` stack:

1. **Rootless podman NATs published-port traffic.** The API logs a host request
   to its own published `:8001` as coming `from 10.89.4.3` — *its own container
   IP*. A host request to `:8081` reaches the frontend Caddy as `10.89.4.5` —
   *Caddy's own IP*. No caller address survives the port-forward boundary.
2. **The frontend Caddy therefore sets `X-Forwarded-For: 10.89.4.5`.** An
   injected `X-Forwarded-For: 9.9.9.9` sent to `:8081` did **not** reach the API
   — the header did not survive the frontend Caddy. (*The exact mechanism is
   unconfirmed: most likely Caddy replaces an untrusted client's XFF because it
   has no `trusted_proxies` configured. Confirm before relying on it.*)
3. **The API's trusted list swallows the whole chain.**
   `OCTBASE_TRUSTED_PROXIES=10.89.4.0/24,178.105.142.1` *contains* `10.89.4.5`.
   `clientFromXFF` (`octbase-api/internal/shared/realip.go`) returns the
   right-most **non**-trusted entry; every entry is trusted, so it returns `""`.
4. **No `X-Real-IP` fallback fires**, so `RemoteAddr` stays the frontend Caddy's
   IP. Every log line for real browser traffic reads `from 10.89.4.5:*`.
5. The per-IP limiter keys **every user in the installation into one bucket**.

`realip.go` is working as designed — `TestRealIP/all_XFF_entries_trusted_yields_none,_keeps_peer`
pins step 3's behavior deliberately. **The bug is upstream: the real IP is
destroyed before the API can see it.** Do not "fix" this by loosening
`clientFromXFF` to return a trusted entry; that would hand out a proxy IP as the
client and defeat the anti-spoofing design.

Corroborating symptom: the `testing` skill already documents "`app` fixture
timing out on login can be the auth rate limit (120/min per IP) after many
consecutive runs" — that is this shared bucket, not a flaky test.

---

## The security trap — read before choosing an option

`rootlessport` rewrites the source address of *everything*, so the frontend Caddy
sees the same peer (`10.89.4.5`) for **both** the legitimate host Caddy **and**
any stranger connecting directly. Peer identity is destroyed, so the frontend
Caddy cannot tell them apart.

That matters because **`:8081` is currently published on all interfaces**
(`podman-compose.yml` publishes `"${FRONTEND_PORT:-8080}:8080"` with no
`${BIND_ADDR}` prefix, and `ss` confirms `LISTEN *:8081`), and the host Caddy
proxies to the **public** `178.105.142.1:8081`. So `http://178.105.142.1:8081` is
reachable from the internet, bypassing the host Caddy's TLS.

**Therefore: configuring the frontend Caddy to trust forwarded headers, while
`:8081` is publicly reachable, creates a spoofing hole** — anyone could send
`X-Forwarded-For: <anything>` straight to `:8081`, forge their client IP, and
bypass rate limiting entirely (and forge audit-log source IPs). Any option that
starts trusting XFF **must** first make the port unreachable except from the
host proxy. Getting this wrong is strictly worse than today's shared bucket.

---

## Option A — close the port, then preserve the XFF chain

Keeps the podman networking as-is. The host Caddy already sees the real client on
`:443` and sets `X-Forwarded-For`; the header survives NAT (only the TCP source
is rewritten). So the client IP is already arriving at the edge — the job is to
make `:8081` private, then stop the frontend Caddy discarding the header, then
narrow the API's trust to match.

Cheap and per-deployment-configurable, but it rests on one premise that is
**not yet confirmed**: that the frontend Caddy will preserve an inbound XFF once
its peer is trusted (see S1 below). Expected end state: XFF at the API reads
`<real client>, 10.89.4.5`, `clientFromXFF` returns `<real client>`, and the API
logs `from <real client>:0`.

**This is the recommended route — see "Proposed plan" for the ordered steps.**

## Option B — preserve source IPs at the podman layer

Make the port forwarder stop NAT-ing, so the frontend Caddy sees real peers and
peer identity is restored (which also removes the trap above).

- podman 5.x rootless: investigate `pasta` (default in 5.x for new networks) vs
  `slirp4netns` with `port_handler=slirp4netns`, which preserves the source IP
  where `rootlessport` does not. This stack currently uses `rootlessport`.
- Trade-offs to weigh and write down: `slirp4netns` port forwarding is slower;
  pasta behavior differs by version; this changes stack networking for every
  deployment, so it must not regress the single-host compose default.
- This is the more "correct" fix but the more invasive one, and it still needs
  step 3 of Option A (narrow the API trust list).

---

## Proposed plan — take Option A, in this order

**Why A over B:** the host Caddy already sees the real client and sets XFF, and
the header survives NAT (only the TCP source is rewritten), so the client IP is
recoverable *without* re-engineering podman's networking for every deployment.
B changes the port forwarder on every stack and buys nothing A doesn't, so keep
it in reserve for if S1 disproves A's premise.

**The ordering is the safety property, not a preference.** Every step that widens
trust comes *after* the step that closes the door. If you stop halfway, stop
after an odd-numbered step — those leave the stack no worse than today. Never
land S4 without S2.

One commit per step, each verified before the next.

### S0 — capture the baseline
Record today's behavior so you can prove you changed it:
`podman logs octbase_dev_octbase-api_1 | grep 'from 10.89.4.5'` and one
`curl -s -o /dev/null http://127.0.0.1:8081/api/v1/health` → note the logged
source. Confirm `https://dev.ocete.ch` serves and `/health` is green
(`stack-health` skill). *No change to the stack.*

### S1 — settle the unknown before building on it
Determine what the frontend Caddy actually does to an inbound XFF: append,
replace, or drop. The image is shell-less `hi/caddy` (~v2.11.4) so you cannot
exec into it — read that version's `reverse_proxy`/`trusted_proxies` docs and
confirm empirically (e.g. a throwaway echo container on the compose network, or
a temporary route to a request-dumping backend on a **disposable** stack).

**This is the gate for the whole plan.** A only works if the frontend Caddy will
*preserve* an inbound XFF once its peer is trusted. If it drops XFF
unconditionally, A is dead — stop and switch to B rather than improvising.

### S2 — close the door (no trust changes yet)
Bind the front door to loopback and repoint the host proxy:
- `podman-compose.yml`: give the frontend's `ports:` the same `${BIND_ADDR}`
  treatment the API and Postgres already have (it is currently
  `"${FRONTEND_PORT:-8080}:8080"`, published on `*`).
- `/etc/caddy/Caddyfile` (root-owned, host-level, **not** in this repo — needs
  sudo and `systemctl reload caddy`): `dev.ocete.ch { reverse_proxy 127.0.0.1:8081 }`
  instead of `178.105.142.1:8081`.

⚠️ Mind the two recorded traps: podman-compose lacks `${VAR:+x}` and passes vars
set-but-empty, and `.env` `POSTGRES_PORT`/`API_PORT` must stay port-only or `up`
fails with "invalid port format" (see `octbase-frontend-caddy-gotchas` and the
`BIND_ADDR port break` note).

**Verify:** `ss -ltnp | grep 8081` shows `127.0.0.1:8081`, not `*:8081`;
`https://dev.ocete.ch` still serves; `:8081` refused from off-host.
**Rollback:** revert the compose port line + host Caddyfile, `systemctl reload
caddy`, recreate the frontend container.

**Land this step even if the rest stalls** — it closes a live TLS bypass
(`http://178.105.142.1:8081` is currently open to the internet) and is worth
doing on its own merits.

### S3 — let the chain through
Add a global options block with `servers { trusted_proxies … }` to **both**
`octbase-frontend/caddy/Caddyfile` and `Caddyfile.tls` (they drift easily —
changing only one is the likely mistake). A Caddy global options block must be
the first, unnamed block in the file, before `:8080 {`. Scope it as tightly as
S1's findings allow.

**Verify:** the API log line for a real browser request becomes
`from <real client ip>:0` — the `JoinHostPort(ip,"0")` signature proving
`RealIP` rewrote it. Expected chain at the API: `<real client>, 10.89.4.5`.

### S4 — narrow the API's trust
`OCTBASE_TRUSTED_PROXIES=10.89.4.0/24,178.105.142.1` trusts the entire compose
network. Once S2 lands, the host proxy arrives over loopback, so the
`178.105.142.1` entry should no longer appear in the chain and can go. Scope the
rest to the frontend Caddy's address.

⚠️ The container IP is **not stable across recreates** — a bare `10.89.4.5/32`
will silently break on the next `up`. Prefer the compose network's documented
subnet or a statically assigned address, and write down which and why.

**Verify:** rate limiting still works (a single IP over budget still gets 429) —
narrowing trust must not accidentally make `clientFromXFF` return `""` again and
send you back to a shared bucket. Re-run S3's check.

### S5 — prove the negative
Demonstrate that a forged `X-Forwarded-For` from an untrusted position **cannot**
set the client IP. This is the step most likely to be skipped, and the one that
distinguishes a real fix from a plausible-looking one. Extend `TestRealIP` with
the real topology's chain shape (`realip.go` is pure and already table-tested),
**and** show it end-to-end against the stack.

### S6 — land it
`go test ./...` + coverage floor, `go-security` sweep, `CHANGELOG.md`
`## Unreleased` `### Security`, and the doc updates listed under Acceptance.

**Report at the end which of S1–S5's checks you actually ran** versus reasoned
about — see "If truly blocked".

---

## Acceptance

- A request from a **known external IP** to `https://dev.ocete.ch` is logged by
  the API as that IP (`from <ip>:0`), not `10.89.4.5`. Show the log line.
- **Two different client IPs get two independent buckets:** one IP exhausting the
  auth budget leaves the other IP serving normally. Demonstrate it end-to-end
  against a running stack, not only in unit tests.
- **Spoofing is not possible:** a request carrying a forged `X-Forwarded-For`
  from an untrusted position cannot set its own client IP. Prove it — this is the
  acceptance criterion most likely to be quietly skipped.
- `:8081` (or whatever the front door binds) is not reachable from off-host if
  Option A is taken; `https://dev.ocete.ch` still serves the app and `/health` is
  green (`stack-health` skill).
- `go test ./...` green + coverage floor held; `go-security` sweep clean.
- `CHANGELOG.md` `## Unreleased` `### Security` entry; `.env.example`
  (`OCTBASE_TRUSTED_PROXIES`), `docs/technical_documentation.md`,
  `docs/hosting-concept.md` and `docs/operations.md` updated to describe the
  supported topology and **exactly which addresses to trust and why**. This is
  per-deployment config, so the docs are load-bearing: a client stack behind a
  different edge proxy needs to know what to set.
- Add a regression test where one is possible (`realip.go` is pure and already
  table-tested — extend `TestRealIP` with the real topology's chain shape).

## If truly blocked

If the environment prevents a decisive test (e.g. no off-host vantage point to
prove the spoofing case, or changing the port handler needs a stack rebuild you
cannot safely do), **stop and report which specific check you could not perform**
rather than declaring it fixed. A wrong trust boundary here is a security
regression that reads as a working feature: rate limiting would look per-client
in tests while any attacker could forge their way around it.
