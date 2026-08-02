You are a senior full-stack engineer with a commercial hat on. Your job is to turn
Octbase from "an admin provisions instances by hand" into a **self-serve product**:
a prospect signs up on the public website, pays, and gets their own Octbase instance
on their own DNS name — automatically. This is the customer-facing and commercial
counterpart to `80_octbase-deployment.md` (the provisioner it drives) and
`81_octbase-operation.md` (which then watches what it created). Read
[`docs/business-plan.md`](../docs/business-plan.md) (pricing/€-per-user) and
[`docs/hosting-concept.md`](../docs/hosting-concept.md) (one isolated stack per tenant)
first — the commercial model already exists; you are building the machinery to sell it.

Ground truth: Octbase itself is **invitation-only** — there is deliberately no public
self-registration inside the app (`octbase-api` has no public "create account"
endpoint; users are seeded or invited). So public signup does **not** belong in
`octbase-api`. It belongs on the marketing website and in a **new, distinct control
plane**. The public marketing site (`ocete.ch`) is a **separate repository** — the
signup form lives there; the order/billing/licensing system is its own service.

## Part 1 — Signup form on the user-facing website (`ocete.ch` repo)

Add a "Start your instance" form to the public site collecting exactly:

| Field | Required | Notes |
|---|---|---|
| First name | yes | |
| Name (surname) | yes | |
| Email | yes | becomes the first SUPER_ADMIN + billing/license contact |
| Company name | **optional** | shown on invoices when present |
| Sitename | yes | slug for the instance; maps 1:1 to the DNS name and the `client_id` |

Requirements:
- **Sitename validation, client- and server-side:** `^[a-z][a-z0-9-]{1,30}$`
  (it becomes a Linux username, a compose project, and a subdomain in
  `80_octbase-deployment.md` — enforce the *same* regex here so an order can never
  produce an invalid `client_id`). Check availability live against the control plane
  (`GET /orders/sitename-available?name=…`) and preview the resulting URL
  (`https://<sitename>.octbase.app`).
- Keep the site's no-heavy-framework ethos and its existing mailer/contact-form
  pattern. Validate + rate-limit + CAPTCHA the endpoint (public write path).
- On submit, POST to the control plane's `POST /orders`; show a "check your email to
  confirm" state. Double opt-in the email before anything is provisioned.

## Part 2 — Order & license management (a distinct service: `octbase-orders/`)

This is intentionally **separate from `octbase-api`** — it is the platform control
plane, not a tenant feature, and it manages many tenants. A small Go service
(reuse the house style/conventions from `octbase-api` — chi, Postgres, stable error
codes, `WriteError` shape) with its **own** database. Responsibilities:

1. **Order lifecycle** — a state machine, each transition logged (audit like
   `auditlog`):
   `PENDING_EMAIL → EMAIL_CONFIRMED → AWAITING_PAYMENT → PAID → PROVISIONING → ACTIVE`
   with side branches `PAYMENT_FAILED`, `SUSPENDED` (non-payment), `CANCELLED`,
   `DEPROVISIONED`. Store the signup fields, chosen plan, sitename→`client_id`, and
   the instance's eventual DNS name.
2. **Provisioning bridge → `80_octbase-deployment.md`.** On `PAID`, the service
   writes `deploy/ansible/clients/<client_id>.yml` from the order (sitename → DNS +
   resources from the plan tier) and triggers `ansible-playbook site.yml -e client=<id>`
   (via a queued job / runner — never block the HTTP request on a multi-minute
   provision). Treat provisioning as idempotent and retryable; move to `ACTIVE` only
   after `check-health.sh --project octbase-<id>` passes, then email the customer their
   URL and first-login/invitation link. On cancellation/non-payment, call
   `deprovision.yml` (data-preserving by default — see that prompt).
3. **Plans / tiers** map directly to the sizing in `hosting-concept.md` §6–§7 and the
   prices in `business-plan.md`: a plan defines seat count and the per-instance Podman
   `resources` block that `80_octbase-deployment.md` consumes. Changing a plan =
   re-render the client vars + re-run the play for that one client.
4. **License management.** Issue a signed license per instance (key covering
   `client_id`, plan, seat limit, valid-until; Ed25519-signed so it can't be forged).
   Decide and document enforcement: the pragmatic path is **control-plane-side** — an
   unpaid/expired license drives the order to `SUSPENDED`, which stops the tenant's
   stack (or shows a billing banner) rather than baking license code into
   `octbase-api`. Track issue/renew/revoke/expiry and surface it on an internal admin
   view. Grace period + dunning before suspension.

## Part 3 — Billing (Switzerland-first: PostFinance, TWINT, cards)

Do **not** hand-roll card handling or store PANs — use a payment service provider and
keep the platform out of PCI scope (hosted checkout / redirect + webhooks). Recommend
and integrate a Swiss-capable PSP that covers all the required methods in one
integration:

- **Datatrans** or **Payrexx** (both Swiss, both do **TWINT + PostFinance Card/Pay +
  Visa/Mastercard/Amex** natively), or **Stripe** (cards + some local methods; TWINT
  support via local payment methods) — pick one, justify it, and abstract it behind a
  `Payer` interface so it can be swapped (mirror how `octbase-api` hides the auth
  `Provider`). List the trade-off: Datatrans/Payrexx for the strongest native Swiss
  method coverage (TWINT, PostFinance) vs. Stripe for DX/reach.
- **Methods to support:** TWINT, PostFinance (Card + Pay), and credit/debit cards.
  Also offer **QR-bill / bank transfer invoice** for company customers who won't pay
  by card — Swiss B2B often wants an invoice; generate a Swiss **QR-bill** PDF.
- **Model:** monthly or annual subscription per instance, seat-based per the plan.
  Handle: hosted-checkout redirect → PSP **webhook** (HMAC/signature-verified, like the
  existing SCM webhook receivers) flips the order to `PAID`/`PAYMENT_FAILED`; recurring
  renewal; failed-payment dunning → grace → `SUSPENDED`; upgrades/downgrades
  (proration); refunds/cancellation → `deprovision.yml`.
- **Invoicing & compliance:** issue sequential invoices, show **Swiss VAT (MwSt)**
  correctly, put the company name on the invoice when supplied, and keep records for
  the statutory retention period. Store only PSP tokens/customer IDs, never raw card
  data. Reconcile PSP payouts against orders.

## Data protection
Signup collects PII (name, email, company). State the retention/erasure policy,
keep the control-plane DB separate from tenant DBs, and ensure a cancellation both
deprovisions the instance and honours a deletion request (nDSG/GDPR).

## Deliverable summary
- **`ocete.ch` (separate repo):** the signup form (5 fields, sitename validation +
  live availability + URL preview), double-opt-in, `POST /orders` wiring.
- **`octbase-orders/`:** Go control-plane service — order state machine, PSP
  integration behind a `Payer` interface (TWINT + PostFinance + cards + QR-bill),
  license issue/track/expire, provisioning bridge that writes `clients/*.yml` and runs
  the ansible play, dunning/suspension, internal admin view, its own migrations/tests.
- **`docs/ordering-and-billing.md`:** the end-to-end flow (signup → confirm → pay →
  provision → active → renew/suspend/cancel), the PSP choice + justification, the
  plan↔resources mapping, and the license/enforcement decision.

## Verification
```bash
# Control-plane service builds + tests (mirror octbase-api conventions):
cd octbase-orders && go build ./... && TEST_DATABASE_URL=... go test ./...
# Full happy path against a sandbox PSP + a test host:
#   POST /orders  -> PENDING_EMAIL
#   confirm email -> EMAIL_CONFIRMED -> AWAITING_PAYMENT
#   PSP sandbox TWINT/card payment -> webhook -> PAID
#   provisioning job runs 80_octbase-deployment.md's site.yml -e client=<slug>
#   check-health.sh --project octbase-<slug> passes -> ACTIVE, welcome email sent
# Negative paths: sitename collision rejected; invalid sitename rejected with the
# same regex as the deploy layer; failed payment -> dunning -> SUSPENDED stops the stack;
# cancellation -> deprovision.yml (data preserved by default).
```
End state: a prospect fills in First name / Name / Email / Company (optional) /
Sitename on `ocete.ch`, pays by TWINT/PostFinance/card, and a monitored Octbase
instance is live at `https://<sitename>.octbase.app` with an active, tracked license —
no manual step in between.
