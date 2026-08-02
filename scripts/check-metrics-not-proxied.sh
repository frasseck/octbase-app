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
# Note we do not attempt to restrict /metrics by source IP: rootless podman NATs
# published-port traffic, so a `not remote_ip 10.0.0.0/8 …` deny sees a private
# container address for every caller and never fires.
set -euo pipefail

cd "$(dirname "$0")/.."

CONFIGS=(
  octbase-frontend/caddy/Caddyfile
  octbase-frontend/caddy/Caddyfile.tls
  octbase-mobile/caddy/Caddyfile
)

fail=0
for f in "${CONFIGS[@]}"; do
  if [ ! -f "$f" ]; then
    echo "metrics-proxy guard: missing config $f" >&2
    fail=1
    continue
  fi
  # Any non-comment line that both routes /metrics and proxies it. Matches the
  # `@backend path … /metrics …` matcher and a direct `reverse_proxy /metrics`.
  if hits=$(grep -nE '^[^#]*(@[A-Za-z_]+ +path|reverse_proxy)[^#]*[[:space:]]/metrics([[:space:]]|$)' "$f"); then
    echo "metrics-proxy guard: $f proxies /metrics — that publishes it:" >&2
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
