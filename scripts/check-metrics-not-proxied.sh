#!/usr/bin/env bash
# Guard: no Caddy config may reverse-proxy /metrics.
#
# The API applies NO auth to /metrics (cmd/octbase-api/main.go registers
# promhttp.Handler() bare), so the route is private only because nothing in front
# of it proxies the path. Prometheus is expected to scrape octbase-api:8000
# directly — see docs/operations.md §Prometheus Metrics.
#
# Why this needs a guard rather than a comment: the invariant spans three
# independently-edited Caddyfiles, and the mobile one is counter-intuitive.
# octbase-mobile is never published to the host, so listing /metrics in its
# @backend set looks harmless — but the front door serves that SPA via
# `handle_path /m/*`, which STRIPS the prefix, so a public request for
# /m/metrics reaches the mobile Caddy as /metrics. That regression shipped and
# left https://<host>/m/metrics world-readable (fixed 2026-07-16); stacks were
# shielded only if the OPTIONAL installation password was on.
#
# Rule of thumb this encodes: any route the front door refuses to proxy must also
# be refused by octbase-mobile's config, or /m/<route> quietly reinstates it.
#
# The rule is deliberately blunt (strengthened 2026-08-02): after stripping
# comments, ANY mention of /metrics in a Caddyfile fails — the invariant is
# that no route config touches the metrics path at all. The previous regex
# only matched /metrics bounded by whitespace/EOL after a `path`/`reverse_proxy`
# token, which let `path /metrics*`, `reverse_proxy /metrics/* …`,
# `handle /metrics {` and `path_regexp ^/metrics` all slip through. The one
# sanctioned shape is a NEGATED matcher (`@name not path … /metrics …`, or a
# bare `not path … /metrics` inside a matcher block): a matcher that refuses
# /metrics cannot be used to route it, and Caddyfile.tls legitimately uses one
# to keep the unauthenticated /metrics 404 out of the basic-auth site gate.
#
# Note we do not attempt to restrict /metrics by source IP: rootless podman NATs
# published-port traffic, so a `not remote_ip 10.0.0.0/8 …` deny sees a private
# container address for every caller and never fires.
set -euo pipefail

cd "$(dirname "$0")/.."

# Discovered, not hand-listed: a new Caddyfile (an env-specific variant, say)
# is guarded the moment it appears, instead of waiting for someone to remember
# this script exists.
mapfile -t CONFIGS < <(find octbase-frontend octbase-mobile -type f -name 'Caddyfile*' \
  -not -path '*/node_modules/*' -not -path '*/dist*' | sort)

# …but discovery alone would happily guard an empty set: if a known config is
# deleted or moved, that must fail loudly, not shrink the scan.
MUST_EXIST=(
  octbase-frontend/caddy/Caddyfile
  octbase-frontend/caddy/Caddyfile.tls
  octbase-mobile/caddy/Caddyfile
)

fail=0
for f in "${MUST_EXIST[@]}"; do
  if [ ! -f "$f" ]; then
    echo "metrics-proxy guard: missing config $f" >&2
    fail=1
  fi
done

for f in "${CONFIGS[@]}"; do
  # Strip full-line and trailing comments (comments may — and do — explain why
  # /metrics is absent), then flag ANY remaining mention of /metrics. Only a
  # negated `not path` matcher line is exempt, per the header.
  if hits=$(sed 's/#.*$//' "$f" | grep -n '/metrics' \
      | grep -vE '^[0-9]+:[[:space:]]*(@[A-Za-z0-9_-]+[[:space:]]+)?not[[:space:]]+path([[:space:]]|$)'); then
    echo "metrics-proxy guard: $f mentions /metrics outside a comment/negation — that risks publishing it:" >&2
    echo "$hits" | sed 's/^/  /' >&2
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  cat >&2 <<'EOF'

/metrics must not be proxied by any Caddy config. The API puts no auth on it.
Scrape octbase-api:8000/metrics directly instead (docs/operations.md
§Prometheus Metrics). Remember /m/<route> reaches octbase-mobile's config with
the /m prefix stripped, so its @backend set is public surface too.
EOF
  exit 1
fi

echo "metrics-proxy guard: clean ✓"
