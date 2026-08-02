# Octbase Business & Cost Model

> Status: Commercial planning document
> Audience: Product/commercial decision-makers, platform owners
> Companion: see [`hosting-concept.md`](hosting-concept.md) for the measured
> capacity, density, and topology this cost model builds on, and
> [`operations.md`](operations.md) for the per-variable runbook.

Density only matters next to a price. This document turns the capacity numbers
from the [hosting concept](hosting-concept.md) (its §6 sizing model) into a
**cost-per-user**, compares providers, and prices the 2,500-user reference
platform. It uses Hetzner Cloud as a concrete, current example — the *method* is
what matters; substitute your own provider's rates.

---

## 1. Cost model (worked example)

### 1.1 Reference instance

The hosting concept's reference node (8 vCPU / 32 GB, see
[`hosting-concept.md`](hosting-concept.md) §6) maps to Hetzner's dedicated-vCPU
**CCX33** (8 dedicated vCPU, 32 GB RAM, 240 GB SSD, 40 TB traffic):

| Plan | vCPU / RAM | €/month (ex-VAT) | €/vCPU |
|---|---|---|---|
| CCX13 | 2 / 8 | 42.99 | 21.50 |
| CCX23 | 4 / 16 | 85.99 | 21.50 |
| **CCX33** | **8 / 32** | **138.49** | **17.31** |
| CCX43 | 16 / 64 | 275.99 | 17.25 |

*Prices: Hetzner, effective 15 June 2026, ex-VAT, ex-IPv4 (+€0.50/mo).*

### 1.2 Cost per user

Density is CPU-bound at ~1 instance (25 users) per vCPU with a host core reserved
([`hosting-concept.md`](hosting-concept.md) §6.1) → 8 instances comfortable, ~12
under light load:

| Scenario | Instances | Users | €/month | **€/user/month** |
|---|---|---|---|---|
| Comfortable (safe) | 8 | 200 | 138.49 | **0.69** |
| Light/bursty | 12 | 300 | 138.49 | **0.46** |

### 1.3 Choosing the node size

Cost-per-user tracks **cost-per-vCPU**, which is *not* flat across the line: small
nodes are penalised because the one reserved host core is a large fraction of the
box. CCX33 and CCX43 are the sweet spot (~€17.3/vCPU); below CCX33 per-user cost
rises sharply (a CCX13 running a single instance is ~€1.72/user). **Use CCX33 as
the scaling unit and scale out, or up to CCX43 for the same per-user economics.**

### 1.4 What the headline number excludes

The €0.46–0.69 figure is **compute only**. A realistic all-in budget must add:

- **Backups** (~+20% of server price), snapshots, block-storage volumes.
- **Object storage** for externalised attachments (Model B/C —
  [`hosting-concept.md`](hosting-concept.md) §6.2).
- A **separate DB host** for shared-DB topologies — Hetzner has no managed
  Postgres, so Model B/C means self-running another node + pgBouncer + HA.
- **Load balancer** (~€5.83/mo), VAT (19% DE → CCX33 ≈ €164.80), and ops/labour.

Folding in backups and modest storage, budget **~€0.70–1.10/user/month** at the
comfortable density.

> **Pricing caveat:** Hetzner protects existing instances at their old rate until a
> rescale. Nodes provisioned before 15 June 2026 may still be on the ~€62.49 CCX33
> rate (~€0.21–0.31/user), but any new node or rescale reprices to €138.49. Re-check
> current rates before budgeting — see sources below.

*Sources: [Hetzner General Purpose (CCX)](https://www.hetzner.com/cloud/general-purpose),
[Hetzner Price Adjustment 15 June 2026](https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/).*

---

## 2. Provider comparison (dedicated vs shared vCPU)

Sticker price per user is meaningless without knowing whether the vCPUs are
**dedicated** or **shared** — the density model
([`hosting-concept.md`](hosting-concept.md) §6) assumes ~1 instance per vCPU
under sustained load, which only holds on dedicated cores. Comparing Hetzner
against STRATO makes the trap concrete:

| Provider / plan | vCPU | RAM | €/mo (regular) | Density (users) | **€/user/mo** |
|---|---|---|---|---|---|
| Hetzner CCX33 | 8 **dedicated** | 32 GB | 138.49 | ~200 (8 inst) | **0.69** |
| STRATO VPS XL | 8 **shared** | 16 GB | 34 | ~175 (≈7 inst)* | **~0.19** |
| STRATO VPS XXL | 12 **shared** | 24 GB | 52 | ~275 (≈11 inst)* | **~0.19** |

*\*Nominal. Shared-vCPU contention (CPU steal, fair-use throttling) and the RAM
ceiling reduce real, predictable density — see caveats below.*

**Key differences:**

- **Dedicated vs shared CPU.** Hetzner CCX gives guaranteed cores, so the
  1-instance-per-vCPU packing holds. STRATO's V-Server line is shared vCPU: cheaper
  on paper (~3–4× lower €/user) but you cannot safely assume the same density under
  sustained load. The honest pairings are *STRATO VPS ↔ Hetzner CX/CPX (both shared)*
  and *STRATO dedicated server ↔ Hetzner CCX (both dedicated)*.
- **No 32 GB virtual plan on STRATO.** The V-Server line stops at 24 GB (VPS XXL).
  A true 32 GB / dedicated-CPU config means STRATO's dedicated-server line — a
  higher cost class that erases much of the apparent advantage.
- **RAM ceiling shapes the topology.** 16/24 GB favours Model B (shared DB) over
  Model A (per-instance DB, ~1 GB/stack), so factor a separate DB host in.
- **Commercial terms.** STRATO's cheap numbers are promo first-term only, with a
  ~12-month commitment and a ~€9 setup fee; budget the *regular* rate. Hetzner bills
  hourly with no commitment.
- **Cloud-native ecosystem.** Hetzner offers object storage, load balancers,
  snapshots and an API — directly relevant to Model B/C (externalised attachments,
  LB, HA). STRATO is weaker here, which matters once you scale past one packed box.

**Guidance:** for the predictable, dense, scalable model the hosting concept is
built around, prefer a **dedicated-vCPU unit (Hetzner CCX33)**. STRATO VPS wins on
sticker price for **light/bursty** single-box workloads that tolerate shared-CPU
variability and a contract. If cheap shared-CPU is the goal, compare against
Hetzner's own CX/CPX line to keep the tooling.

*Sources: [STRATO VPS](https://www.strato.de/server/vps/),
[STRATO Linux vServer](https://www.strato.de/server/linux-vserver/).*

---

## 3. Platform-scale cost — the 2,500-user reference

The [hosting concept](hosting-concept.md) §15 specifies a concrete end-state for
**100 client tenants × 25 users = 2,500 users** (a worked instance of Model C with
the shared singletons factored out once). Pricing its bill of materials
(hosting-concept §15.3) at Hetzner's new rates:

| Tier | Unit × count | €/mo |
|---|---|---|
| App fleet | CCX43 × 8 | ~2,207.92 |
| DB primary + fallback | CCX33 × 2 | ~276.98 |
| Website edge | CX22 × 2 | ~12 |
| Load balancer | LB × 2 | ~11.66 |
| Object storage | usage-based | ~20–50 |
| **Compute subtotal** | | **~€2,530** |
| Backups (~+20% of servers) | | ~€500 |
| **All-in** | | **~€3,030/mo** |

At 2,500 users that is **~€1.0–1.2 / user / month all-in** — consistent with the
§1.4 "fold in backups, DB host and storage" guidance, and the price of buying
**HA** (DB failover, N+1 app fleet, redundant edge) on top of raw single-node
density.

---

*Cost figures derive from the measured resource baseline in
[`hosting-concept.md`](hosting-concept.md) §3 (8 vCPU / 32 GB host). Re-measure and
re-price against your own target hardware and current provider rates before
committing.*
