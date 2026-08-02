# Octbase — Commercial Plan (Solo Base Case)

> Status: **Primary commercial plan** · single-founder base case · €3.95/seat
> Companion: [`business-plan.md`](business-plan.md) (cost model),
> [`hosting-concept.md`](hosting-concept.md) (topology & density),
> [`growth-to-20-plan.md`](growth-to-20-plan.md) (engineering roadmap).

**This is Octbase's primary commercial plan: a single founder, pricing at €3.95 per
seat.** The platform-scale numbers (500+ clients, ~€590k+ ARR at €3.95) are the
*upside* case — they require a **team**, and **§5 lays out the path from solo to a
five-person company**. This document sets the realistic solo trajectory, commits the
price, adds the **reseller/MSP channel**, and then the team-scaling plan that turns
the upside into a target.

---

## 1. Premise — one person changes the math

The ceiling on a solo operation is **not** the product or the infrastructure (both
scale fine) — it is the founder's own time, split across every function. Two hard
limits dominate:

1. **Distribution is the bottleneck, not code.** The product largely exists; getting
   in front of buyers (content, SEO, channel) is the slow part and the thing
   technical solo founders most often underestimate.
2. **Support scales with clients.** A team tool generates tickets. At a low per-seat
   price you cannot hand-hold — everything must be self-serve + docs + automation, or
   you hit a wall around **~100–150 active client orgs** where support consumes all
   your time.

**Offsetting upside:** a solo cost base is tiny (own modest salary + ~€2k/mo infra &
tools), so break-even comes early — roughly **50–70 clients covers a solo income**,
versus ~85–170 once a team is on payroll.

---

## 2. Realistic 3-year trajectory

Assumptions: a working O2 platform (per-tenant database; see
[`hosting-concept.md §16`](hosting-concept.md)); **~15 seats/client average** (SMB
self-serve skews smaller than the 20-seat team-plan assumption); steady but
unspectacular marketing; low churn.

Per-client revenue = **€19 base + seats × €3.95** (≈ €78/mo at 15 seats):

| End of | Clients | Seats | MRR | ARR |
|---|---|---|---|---|
| **Year 1** | ~15 | ~225 | ~€1,175 | ~€14k |
| **Year 2** | ~50 | ~750 | ~€3,910 | ~€47k |
| **Year 3** | ~100 | ~1,500 | ~€7,825 | ~€94k |

**Realistic landing: ~100 client orgs, ~1,500 seats, ~€94k ARR.** After infra
(~€1.6k/mo at that scale) that is ~€6.2k/mo gross — a real, if modest, solo income.

### Outcome bands (Year 3)

| Scenario | Clients | ARR | Meaning |
|---|---|---|---|
| **Stall** — no distribution traction | <25 | ~€20–25k | Side income; the common solo failure mode |
| **Realistic** — steady self-serve + content | ~80–120 | ~€75–110k | Replaces a salary — a genuine success |
| **Good** — SEO compounds or a reseller deal lands | ~150 | ~€140k | Strong solo business; nearing the support wall — hire or automate hard |

The **500-client / ~€590k** platform target is the **upside** case — it needs a
**team** (the GTM and support volume one person can't cover), so this plan treats it
as a stretch goal and **§5 lays out the path there**. The realistic solo three-year
result is roughly **a fifth of that**.

---

## 3. Pricing — €3.95 per seat (committed)

**The committed price is €19 base + €3.95 per seat**, leading on hosting
quality/sovereignty rather than price. Why €3.95 rather than a lower (e.g. €2.95) cap:

- **Affordability barely moves.** A 15-seat team pays €78/mo vs €63 at €2.95 — both a
  fraction of Jira (~€105–120 for the same seats). €3.95 is still clearly budget-tier.
- **The "bang for the buck" contest is won by positioning, not price.** A buyer
  choosing on features-per-euro is lost at €2.95 *and* €3.95 (a feature-rich rival at
  €5 beats both). A buyer choosing on **isolation / sovereignty / quality hosting**
  pays €3.95 happily. Price for the buyers you can win, and capture ~25 % more from
  them for the same support load.
- **Higher ARPU is strategically right for a solo founder.** Fewer clients for the
  same income means less support — and support is the binding constraint. €3.95 lifts
  the realistic Year-3 result from ~€76k to ~€94k ARR (or lets you hit a target income
  with ~25 % fewer clients to serve).
- **Don't straddle.** Being simultaneously "the cheapest" and "the quality one" is a
  weak position. At €3.95 you are cleanly *"your data, done right — still a fraction of
  Jira,"* which is more defensible and avoids a race to the bottom on a low-ARPU model.

A premium **"Dedicated instance" (O1)** tier above the cap remains available for
clients needing a fully isolated Postgres *instance* — another ARPU lever that suits a
solo operator (fewer, larger, higher-value accounts).

---

## 4. The reseller / MSP channel — the solo lever

The single biggest lever on the solo numbers is **not doing distribution and support
yourself.** Agencies, IT service providers and MSPs already own client relationships
and first-line support capacity. Let them resell Octbase.

**Why it fits Octbase specifically:** the architecture is *already* per-tenant
isolated stacks + databases (O1/O2). "**White-label, isolated instances for your
clients**" is therefore a natural product, not a retrofit — each partner-client gets
its own dedicated database under the partner's brand.

**Three structures (can be combined):**

| Model | How it works | Best for |
|---|---|---|
| **Referral / rev-share** | Partner refers; you bill the client; partner earns ~20–30 % recurring | Low-commitment partners, fastest to start |
| **Wholesale** | Partner buys seats at a discount and sets their own retail price; partner owns the customer | Partners who want margin + control |
| **White-label** | Branded, dedicated instances provisioned per partner-client | MSPs productising "managed work-management" |

**Why it breaks the solo ceiling:**

- **Distribution is outsourced** — partners sell into their existing book; your CAC
  approaches zero.
- **First-line support is outsourced** — the partner handles their clients; you keep
  only platform/second-line. This directly relieves the ~100–150-client support wall.
- **Margin barely suffers.** Even granting a partner 25 % at €3.95, your net is ~€2.96
  /seat → ~€2.41 contribution after €0.55 COGS — essentially the same as a direct sale
  at €2.95, but with CAC and support offloaded.
- **A handful of active partners** bringing 10–30 client orgs each can move you into
  the "Good" band (and beyond) without a single direct hire — the only realistic solo
  path past ~150 clients.

**Trade-off:** you give up the direct customer relationship and some price control, and
you take on partner enablement (onboarding docs, a provisioning API/portal, a partner
agreement). For a solo founder, that is a good trade — partner enablement is *build*
work (your strength), whereas direct sales + support at volume is the work you cannot
personally scale.

---

## 5. Scaling from solo to a five-person team

The solo plan hits three named ceilings: **distribution** (§1), the **~150-client
support wall** (§1), and **fleet operations/reliability** as the O2 platform shards
and clients demand SLAs. The team is built specifically to remove those ceilings —
each hire buys back a constraint the founder cannot personally scale past.

The target organisation is **five people**: founder (lead engineer), product
owner / sales, devops engineer, and two full-stack engineers.

### 5.1 What each role unlocks

| Role | Constraint it removes | What it unlocks |
|---|---|---|
| **Founder — lead engineer** | — (sets technical direction) | Architecture, the hard platform problems, hiring/standards; steps out of day-to-day ops and support as the others land |
| **Product owner / sales** | Distribution (the #1 solo bottleneck) | A real acquisition engine + roadmap ownership; frees the founder from customer-facing and prioritisation work. **The highest-leverage first hire** |
| **DevOps engineer** | Fleet ops & reliability | Owns provisioning automation, HA Postgres + sharding (O2 past one DB server), monitoring, on-call, SLA — the work that becomes full-time around a few hundred clients |
| **Full-stack engineer ×2** | Feature velocity + support depth | Close the "bang-for-the-buck" feature gap, build **partner-enablement / white-label** (the §4 channel), and absorb support escalations so growth isn't support-bound |

### 5.2 Hiring sequence & triggers

Hire in constraint order, each gated by ARR so payroll stays funded:

| # | Hire | Trigger (≈) | Headcount | Loaded opex/yr* |
|---|---|---|---|---|
| 1 | Product owner / sales | ~€120k ARR, product stable, solo wall in sight | 2 | ~€180k |
| 2 | DevOps engineer | ~€300k ARR / ~250 clients; HA, sharding, SLA demands | 3 | ~€290k |
| 3–4 | 2× full-stack engineer | ~€550k ARR / ~500 clients; channel + roadmap backlog | 5 | ~€640k |

*\*Fully-loaded, assuming **remote DACH/EU hiring** (~€100–120k/person blended) plus
non-people opex (infra at scale, tooling, marketing, admin). **Zurich-only hiring is
~50 % more expensive** and a major driver of break-even — the grounded salary bands
and their impact are in §5.7.*

### 5.3 When the team is sustainable

Gross margin is healthy (~**80 %** at €3.95 / 20 seats), so the model *can* carry a
team — but only at meaningful ARR. Each €100k loaded hire needs ~€125k of incremental
ARR to self-fund. Sustainability points:

| Headcount | Loaded opex | ARR to break even (opex ÷ 0.8) | ≈ Clients (20 seats) |
|---|---|---|---|
| 1 (solo, lean draw) | ~€80k | ~€100k | ~85 |
| 2 (+ PO/sales) | ~€180k | ~€225k | ~190 |
| 3 (+ devops) | ~€290k | ~€360k | ~310 |
| **5 (full team, DACH-remote)** | **~€640k** | **~€800k** | **~680** |

So the **full five-person team needs ~€800k ARR (~680 clients)** to break even
(DACH-remote) — *beyond* the 500-client "upside" marker. The team is therefore the
**vehicle to push past that line** toward €800k–1.2M ARR, not something the 500-client
milestone already funds. A **Zurich-based team raises that bar to ~€1.15M ARR
(~980 clients)** — see §5.7.

### 5.4 Funding the build

Two routes to get from the solo ~€94k ARR to a self-sustaining five-person team:

- **Bootstrapped (slower, lower risk):** hire only as ARR clears each §5.3 threshold,
  reinvesting gross profit. Reaches the full team around **Year 6**. No dilution; the
  founder controls pace; the risk is being out-run by a funded competitor.
- **Raised (faster, higher risk):** a **~€500k–1M seed** funds hiring *ahead* of
  revenue, betting the channel + product reach ~€800k ARR before the runway ends.
  Compresses the build to **~Year 5**. The risk is a missed ramp with a payroll
  already in place — the §5.2 triggers become the milestones investors underwrite.

### 5.5 The six-year arc

Continuing from the solo Years 1–3 (avg seats/client rises from ~15 self-serve toward
~20 as sales lands larger accounts):

| End of | Clients | Headcount | ≈ ARR | EBITDA | Note |
|---|---|---|---|---|---|
| Year 3 | ~100 | 1 | ~€94k | break-even-ish | Solo base case |
| Year 4 | ~250 | 2→3 | ~€260k | negative (invest) | PO/sales drives acquisition; devops added late |
| Year 5 | ~450 | 4 | ~€510k | ~break-even | Engineers ramp; channel live |
| Year 6 | ~700 | 5 | ~€820k | **positive** | Full team sustainable; channel-led scale toward €1M+ |

Years 4–5 are a deliberate **investment phase** (negative EBITDA funded by reinvested
profit or the seed); Year 6 turns profitable as ARR clears the ~€800k bar (DACH-remote).

### 5.6 Risks specific to scaling the team

- **The PO/sales hire is the linchpin** — distribution is the binding constraint, so a
  weak first hire stalls the entire thesis. Hire for this before engineers.
- **Swiss salary cost** can break the math — keep the team **remote across DACH/EU**;
  an all-Zurich team is ~50 % more expensive and raises break-even by ~**300 clients**
  (§5.7).
- **Founder transition** — from doing-everything to leading; delegating ops (devops)
  and customers (PO) is the cultural shift that makes or breaks the step-up.
- **Hiring ahead of revenue** (raised path) converts a low-risk solo business into a
  burn-rate business — only raise against the §5.2 triggers actually being hit.

### 5.7 Salary reality check — DACH-remote vs Zurich

The opex figures above assume **remote DACH/EU hiring**. Grounded in 2025/26 market
data, fully-loaded annual cost per role (gross + employer on-costs; DE/AT loading
~1.20–1.30×, CH ~1.15–1.18× on a much higher base, 13th-month included):

| Role | DACH-remote (DE/AT), loaded €/yr | Zurich, loaded (≈ €/yr) |
|---|---|---|
| Lead engineer (founder) | ~€95–110k | CHF ~140k (~€145k) |
| Product owner / sales | ~€95–115k (+ commission) | CHF ~165k (~€170k) |
| DevOps engineer | ~€110–125k | CHF ~150k (~€155k) |
| Full-stack engineer (×2) | ~€95–105k each | CHF ~170k each (~€175k) |
| **People cost (5)** | **~€520k** | **~CHF 800k (~€820k)** |
| Non-people opex | ~€120k | ~€120k |
| **Fully-loaded opex** | **~€640k** | **~€940k** |

**Impact on break-even** (at ~80 % gross margin, €1,176 ARR/client at 20 seats):

| Hiring base | Opex/yr | Break-even ARR | Break-even clients |
|---|---|---|---|
| **DACH-remote** | ~€640k | ~€800k | **~680** |
| **Zurich-based** | ~€940k | ~€1.18M | **~1,000** |

A Zurich team costs ~€300k/yr more and needs ~**300 extra clients** (~€350k more ARR)
to break even — roughly the difference between a reachable target and a much harder
one. **Recommendation: hire remote across DACH/EU**, with at most the founder (and
perhaps the customer-facing PO/sales role for local market access) based in
Switzerland. A hybrid (CH founder + remote DE/AT team) lands near ~€700k opex.

*Salary sources (2025/26): software engineer — Switzerland median ~CHF 130k, Zurich
avg ~CHF 128k (levels.fyi, whatisthesalary.com); Germany median ~€81.5k. DevOps —
Germany avg ~€85k / senior ~€105k (Glassdoor, PayScale); Switzerland avg ~CHF 110k.
Product owner — Switzerland avg ~CHF 129–135k; Germany avg ~€61k (Glassdoor,
worldsalaries.com). Figures are mid-senior bands; treat as planning estimates.*

---

## 6. Takeaways

1. **Realistic solo 3-year target: ~100 clients, ~1,500 seats, ~€94k ARR** —
   enough to replace a salary, roughly a fifth of the team-scale upside.
2. **Price at €3.95** (committed), lead with sovereignty/quality, and keep an O1
   premium tier — higher ARPU means fewer clients to support for the same income.
3. **The reseller/MSP channel (especially white-label) is the decisive lever** — it is
   the only way one person plausibly reaches and passes the ~150-client support wall,
   and it plays to a solo technical founder's strengths (build a platform + partner
   API, not a sales org).
4. **Scaling to five people removes the solo ceilings in order** — PO/sales
   (distribution) → devops (reliability) → 2× full-stack (velocity + support). The
   full team needs **~€800k ARR (~680 clients)** to sustain when hired remote across
   DACH/EU — and **~€1.18M (~1,000 clients) if Zurich-based** (§5.7), so location is a
   first-order lever. Reached ~Year 6 bootstrapped or ~Year 5 with a €0.5–1M seed.
