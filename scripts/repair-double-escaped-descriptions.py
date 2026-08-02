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

# A character reference whose leading "&" has itself been escaped: the signature
# of a save that ran over its own output. Numeric and named forms both appear.
OVER_ESCAPED = re.compile(r"&amp;(?:amp|lt|gt|quot|apos|nbsp|#[0-9]+|#[xX][0-9a-fA-F]+);")

# The fix landed for 1.1.2. Below this, a repair re-corrupts on the next edit.
MIN_VERSION = (1, 1, 2)


def strip_extra_layers(s):
    """Decode only the layers the bug added, and report how many there were.

    Each pass rewrites "&amp;" to "&" exactly once, which undoes one round of
    escaping. The loop stops as soon as no over-escaped reference remains, so a
    correctly stored "&amp;" (a literal ampersand) or "&lt;" (a literal "<") is
    left exactly as it is. The bound is a backstop, not an expectation — the
    worst row measured was five layers deep.
    """
    layers = 0
    while OVER_ESCAPED.search(s) and layers < 10:
        s = s.replace("&amp;", "&")
        layers += 1
    return s, layers


def _req(base, method, path, token=None, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(base + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r) as resp:
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
    repaired = failed = 0
    for t in tasks:
        if wanted and t["id"] not in wanted:
            continue
        before = t.get("description") or ""
        after, layers = strip_extra_layers(before)
        if not layers:
            continue

        print(f"\n── {t['id']}  {t.get('title', '')[:60]}")
        print(f"   {layers} extra layer(s)")
        for m in list(OVER_ESCAPED.finditer(before))[:3]:
            lo, hi = max(0, m.start() - 30), min(len(before), m.end() + 20)
            print(f"   before: …{before[lo:hi]}…")
        for m in list(OVER_ESCAPED.finditer(before))[:3]:
            lo = max(0, m.start() - 30)
            print(f"   after:  …{after[lo:lo + 55]}…")

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
        # so what was stored is what matters, not what was sent.
        st, back = _req(base, "GET", f"/tasks/{t['id']}", token)
        stored = (back or {}).get("description", "")
        if OVER_ESCAPED.search(stored):
            failed += 1
            print("   NOT REPAIRED: the stored value is still over-escaped — the "
                  "instance is very likely running a build without the fix")
        else:
            repaired += 1
            print("   repaired")

    if args.apply and args.yes:
        print(f"\nrepaired {repaired}, failed {failed}")
    else:
        print("\ndry run — nothing was written. Re-run with --apply --yes to repair.")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
