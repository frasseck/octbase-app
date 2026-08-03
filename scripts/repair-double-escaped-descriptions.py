#!/usr/bin/env python3
"""Repair task descriptions that the pre-1.1.2 sanitizer escaped more than once.

WHY THIS EXISTS
---------------
Until commit 0b0d928 the task write path re-escaped its own output, so saving a
description you had not changed still rewrote it: "&" gained an "&amp;" layer
per save, and an arrow typed as "->" degraded to "-&gt;", then "-&amp;gt;", and
onwards. The fix makes sanitizing idempotent, which stops the damage — but it
deliberately does not undo it, because decoding blindly would also unravel
content a user wrote on purpose and would turn historic escaped markup back into
live markup. This script is the repair, run once, under review.

READ THIS BEFORE RUNNING IT
---------------------------
1. **Run it only against an instance that already has the idempotence fix.**
   On an older build the repair holds until the row is next edited and then
   re-corrupts, which is worse than leaving it: it looks fixed.
   `--check-version` (default) refuses to apply below the minimum version.
2. **One layer is correct and must not be touched.** A literal ampersand is
   stored as "&amp;" and a literal "<" as "&lt;". Only a reference whose own "&"
   was escaped again — "&amp;lt;", "&amp;amp;" — is damage. The decoder below
   strips exactly those layers and stops; it never decodes a well-formed
   single-escaped value.
3. **It is ambiguous in one case, by nature.** Someone who deliberately wrote
   the literal text "&lt;" (to show markup) stores it correctly as "&amp;lt;",
   which is indistinguishable from a double-escaped "<". That is why the default
   is a dry run that prints a diff, and why --apply needs --yes: a human decides.

USAGE
-----
    # 1. See what would change (default; writes nothing)
    ./repair-double-escaped-descriptions.py --base https://host/api/v1 \\
        --email you@example.com --password-file ~/.secret --project <uuid>

    # 2. Apply, after reading the diff
    ./repair-double-escaped-descriptions.py ... --apply --yes

    # 3. Or one row at a time, which is the safest way through the ambiguous ones
    ./repair-double-escaped-descriptions.py ... --apply --yes --task <task-uuid>

Scope: task descriptions only. Wiki pages are sanitized by internal/docs, which
had the idempotent escapeText from the start — verified clean on the dogfooding
instance (1 page, 0 corrupted fields) rather than assumed. Titles are not
sanitized and were never affected, contrary to the original report.
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request

# A character reference whose leading "&" has itself been escaped, once per
# damaging save: the signature of a save that ran over its own output. Numeric
# and named forms both appear. Group 1 is the run of extra "amp;" layers (one
# per save), group 2 the reference body the layers were wrapped around.
OVER_ESCAPED = re.compile(
    r"&((?:amp;)+)(amp|lt|gt|quot|apos|nbsp|#[0-9]+|#[xX][0-9a-fA-F]+);")

# The fix landed for 1.1.2. Below this, a repair re-corrupts on the next edit.
MIN_VERSION = (1, 1, 2)


def strip_extra_layers(s):
    """Decode only the layers the bug added, and report the worst depth seen.

    Each over-escaped reference is rewritten IN PLACE — "&amp;amp;gt;" becomes
    "&gt;" — and nothing else in the string is touched, so a correctly stored
    "&amp;" (a literal ampersand) or "&lt;" (a literal "<") elsewhere in the
    same row survives exactly as it is. (An earlier version ran a global
    s.replace("&amp;", "&") per pass, which decoded every legitimate entity in
    any row that contained one damaged reference — the exact violation of the
    docstring's own guarantee.)

    Returns (repaired, worst): `worst` is the deepest per-reference layer count
    (0 when nothing matched, so the caller's truthiness check still works).
    References in one row can sit at different depths — the count is per
    reference, not per row. The worst row measured was five layers deep.
    """
    worst = 0

    def undo(m):
        nonlocal worst
        worst = max(worst, len(m.group(1)) // 4)  # each layer is one "amp;"
        return "&" + m.group(2) + ";"

    return OVER_ESCAPED.sub(undo, s), worst


# Mirror of the server's write path, so the read-back can verify the stored
# value against what the repair SHOULD have produced rather than merely "no
# longer matches OVER_ESCAPED". SanitizeDescriptionHTML
# (octbase-api/internal/workmanagement/sanitize.go) keeps allowlisted tags,
# runs shared.EscapeText (octbase-api/internal/shared/htmlsafe.go) over the
# text runs between them, and TrimSpaces the result. The values this script
# sends are the server's own sanitized output with over-escape layers removed,
# so the tags pass through verbatim; if a sent value somehow contains markup
# the sanitizer would rewrite, expected_stored and the store disagree and the
# row is reported NOT REPAIRED — which is the safe direction.

# Same shape as sanitize.go's tagRe: one HTML tag, quoted attrs tolerated.
_TAG = re.compile(r"(?s)<(/?)([a-zA-Z][a-zA-Z0-9]*)((?:[^>\"']|\"[^\"]*\"|'[^']*')*)>")

# Same shape as htmlsafe.go's entityRe: an already-encoded reference, which
# idempotent escaping preserves instead of re-encoding its "&".
_ENTITY = re.compile(r"&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);")


def escape_once(s):
    """Python mirror of shared.EscapeText: idempotent text-run escaping."""
    out = []
    i = 0
    while i < len(s):
        c = s[i]
        if c == "&":
            m = _ENTITY.match(s, i)
            if m:
                out.append(m.group(0))
                i = m.end()
                continue
            out.append("&amp;")
        elif c == "<":
            out.append("&lt;")
        elif c == ">":
            out.append("&gt;")
        else:
            out.append(c)
        i += 1
    return "".join(out)


# Attribute values do NOT get the idempotent EscapeText treatment: sanitizeAttrs
# decodes one entity layer (shared.DecodeEntities) and then re-escapes with the
# deliberately non-idempotent shared.EscapeAttr. That round-trip is the identity
# only for references whose decoded character re-encodes to the same spelling —
# exactly &amp; &lt; &gt; &quot; &#39;. Any other repaired reference inside a
# tag's attributes (&apos; &nbsp; hex forms, other numerics) would be rewritten
# by the server on the repair PUT: the read-back would flag the row NOT
# REPAIRED, but the attribute would have been mutated anyway. Such rows are
# detected up front and skipped — the dry run predicts it, and nothing writes.
ATTR_FIXED_POINT = {"amp", "lt", "gt", "quot", "#39"}


def risky_attr_refs(s):
    """Over-escaped references inside a tag whose repaired spelling the attr
    pipeline would not preserve. Returns the matches, so the caller can say
    which references make the row hand-repair territory."""
    spans = [(m.start(), m.end()) for m in _TAG.finditer(s)]
    return [m for m in OVER_ESCAPED.finditer(s)
            if any(a <= m.start() < b for a, b in spans)
            and m.group(2) not in ATTR_FIXED_POINT]


def expected_stored(sent):
    """What the server's sanitizer stores for `sent`: tags kept, text runs
    escaped once (idempotently), surrounding whitespace trimmed."""
    out, last = [], 0
    for m in _TAG.finditer(sent):
        out.append(escape_once(sent[last:m.start()]))
        out.append(m.group(0))
        last = m.end()
    out.append(escape_once(sent[last:]))
    return "".join(out).strip()


def _req(base, method, path, token=None, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(base + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, raw.decode(errors="replace")


def version_tuple(v):
    parts = re.findall(r"\d+", v or "")
    return tuple(int(p) for p in parts[:3]) if len(parts) >= 3 else None


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base", required=True, help="API base, e.g. https://host/api/v1")
    ap.add_argument("--email", required=True)
    ap.add_argument("--password-file", required=True,
                    help="file holding the password (avoids it landing in shell history)")
    ap.add_argument("--project", required=True, help="project UUID")
    ap.add_argument("--task", action="append", default=[],
                    help="repair only this task id; repeatable")
    ap.add_argument("--apply", action="store_true", help="write the repairs (default: dry run)")
    ap.add_argument("--yes", action="store_true", help="required alongside --apply")
    ap.add_argument("--no-check-version", action="store_true",
                    help="apply even below %s — you are asserting the idempotence "
                         "fix is deployed" % ".".join(map(str, MIN_VERSION)))
    args = ap.parse_args()

    base = args.base.rstrip("/")
    password = open(args.password_file).read().strip()

    st, body = _req(base, "POST", "/auth/login", body={"email": args.email, "password": password})
    if st != 200:
        sys.exit(f"login failed: {st} {body}")
    token = body["accessToken"]

    st, ver = _req(base, "GET", "/version", token)
    running = (ver or {}).get("version", "?")
    vt = version_tuple(running)
    print(f"instance {base} reports version {running}")
    if args.apply and not args.no_check_version and (vt is None or vt < MIN_VERSION):
        sys.exit(
            f"refusing to apply: this instance reports {running}, and the idempotence "
            f"fix ships in {'.'.join(map(str, MIN_VERSION))}. Repairing an older build "
            "leaves rows that re-corrupt on their next edit, which looks fixed and is "
            "not. Deploy first, or pass --no-check-version if you know better."
        )

    tasks, page = [], 0
    while True:
        st, b = _req(base, "GET", f"/projects/{args.project}/tasks?page={page}&size=200", token)
        if st != 200:
            sys.exit(f"listing tasks failed: {st} {b}")
        items = b.get("items", b) if isinstance(b, dict) else b
        if not items:
            break
        tasks.extend(items)
        if len(items) < 200:
            break
        page += 1

    wanted = set(args.task)
    missing = wanted - {t["id"] for t in tasks}
    if missing:
        print("WARNING: --task id(s) not found in this project's task list "
              "(nothing was checked for them): " + ", ".join(sorted(missing)),
              file=sys.stderr)

    repaired = failed = attr_skipped = 0
    for t in tasks:
        if wanted and t["id"] not in wanted:
            continue
        before = t.get("description") or ""
        after, worst = strip_extra_layers(before)
        if not worst:
            continue

        matches = list(OVER_ESCAPED.finditer(before))
        print(f"\n── {t['id']}  {t.get('title', '')[:60]}")
        print(f"   {len(matches)} over-escaped reference(s), deepest {worst} extra layer(s)")
        risky = risky_attr_refs(before)
        if risky:
            refs = ", ".join(sorted({m.group(0) for m in risky}))
            print(f"   SKIPPED (attr-level damage): {len(risky)} reference(s) inside "
                  f"tag attributes ({refs}) would be rewritten by the server's "
                  "attribute pipeline on write — repair this row by hand")
            attr_skipped += 1
            continue
        # Excerpts around the first three matches. The "after" windows are
        # sliced with offsets valid for the AFTER string: each replacement
        # shrinks the text by the length of the stripped layers (group 1), so
        # the cumulative shrinkage of every earlier match is subtracted.
        for m in matches[:3]:
            lo, hi = max(0, m.start() - 30), min(len(before), m.end() + 20)
            print(f"   before: …{before[lo:hi]}…")
        shift = 0
        for i, m in enumerate(matches):
            if i < 3:
                apos = m.start() - shift
                repl_end = apos + len(m.group(0)) - len(m.group(1))
                lo, hi = max(0, apos - 30), min(len(after), repl_end + 20)
                print(f"   after:  …{after[lo:hi]}…")
            shift += len(m.group(1))
        if len(matches) > 3:
            print(f"   … {len(matches) - 3} more match(es) not shown")

        if not (args.apply and args.yes):
            continue
        # PATCH needs the current version; DONE/ARCHIVED tasks refuse the field
        # entirely (422 TASK_IMMUTABLE) and have to be reopened by a human first
        # — this script deliberately does not reopen anything.
        st, b = _req(base, "PATCH", f"/tasks/{t['id']}", token,
                     {"description": after, "version": t["version"]})
        if st != 200:
            failed += 1
            print(f"   NOT REPAIRED: {st} {b}")
            continue
        # Never trust the status code — read it back. The write path sanitizes,
        # so what was stored is what matters, not what was sent — and "no longer
        # matches OVER_ESCAPED" is a weaker claim than "stored what the repair
        # computed", so compare against the mirrored write path exactly.
        st, back = _req(base, "GET", f"/tasks/{t['id']}", token)
        stored = (back or {}).get("description", "")
        expect = expected_stored(after)
        if stored != expect:
            failed += 1
            if OVER_ESCAPED.search(stored):
                print("   NOT REPAIRED: the stored value is still over-escaped — the "
                      "instance is very likely running a build without the fix")
            else:
                print("   NOT REPAIRED: the stored value differs from the expected "
                      "repair result — the server rewrote the value on write; "
                      "inspect this row by hand")
                print(f"     expected: {expect[:120]!r}")
                print(f"     stored:   {stored[:120]!r}")
        else:
            repaired += 1
            print("   repaired")

    if args.apply and args.yes:
        print(f"\nrepaired {repaired}, failed {failed}, "
              f"skipped (attr-level damage, repair by hand) {attr_skipped}")
    else:
        print("\ndry run — nothing was written. Re-run with --apply --yes to repair.")
    return 1 if failed or missing or attr_skipped else 0


if __name__ == "__main__":
    sys.exit(main())
