#!/usr/bin/env bash
# Stop hook: remind Claude to update CHANGELOG.md when core code changed.
# Blocks the stop once (stop_hook_active guards against loops) if uncommitted
# changes touch octbase-api/ or octbase-frontend/ without a CHANGELOG.md change.
set -u

input=$(cat)

# Don't re-block if we already blocked once this stop cycle (no jq on this host).
if printf '%s' "$input" | grep -q '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

root="${CLAUDE_PROJECT_DIR:-.}"
status=$(git -C "$root" status --porcelain 2>/dev/null) || exit 0

core_changed=false
changelog_changed=false
printf '%s\n' "$status" | grep -qE '(octbase-api|octbase-frontend)/' && core_changed=true
printf '%s\n' "$status" | grep -q 'CHANGELOG\.md' && changelog_changed=true

if [ "$core_changed" = true ] && [ "$changelog_changed" = false ]; then
  cat <<'EOF'
{"decision":"block","reason":"Uncommitted changes touch octbase-api/ or octbase-frontend/ but CHANGELOG.md was not updated. Per CLAUDE.md, add an entry under '## Unreleased' (Added / Changed / Security) describing the behavior change. If the changes are genuinely not changelog-worthy (tests, comments, formatting only) or were not made by you in this session, you may finish without an entry."}
EOF
fi

exit 0
