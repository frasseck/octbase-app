#!/usr/bin/env python3
"""Drives the running Octbase stack (desktop + mobile SPA) with Playwright
against system Chrome, and screenshots the result.

Requires a stack already up (see SKILL.md "Bring up the stack"). Uses the
Playwright install in octbase-frontend/tests/.venv — run this script with
that venv's python, not the system one.

Usage:
    driver.py desktop [--ui-base URL] [--out DIR]
    driver.py mobile  [--ui-base URL] [--out DIR]

`desktop` logs in, lands on the dashboard, opens the Demo Project board.
`mobile`  logs in on the /m/ SPA, lands on the dashboard, opens the bottom nav board tab.
Both flows finish with `console --errors`-equivalent: any page console
"error" entries are printed, and a non-zero exit means one was seen.
"""
import argparse
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

DEMO_EMAIL = "demo@octbase.dev"
DEMO_PASSWORD = "demopass1234"


def run_flow(mode: str, ui_base: str, out_dir: Path) -> int:
    out_dir.mkdir(parents=True, exist_ok=True)
    console_errors: list[str] = []

    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome", headless=True)
        if mode == "mobile":
            # Caddy device-routes by User-Agent: a non-phone UA hitting /m/
            # gets 302'd back to /. Must emulate a phone to actually land on
            # the mobile SPA (see octbase-frontend/caddy/Caddyfile @phoneEntry).
            context = browser.new_context(**p.devices["iPhone 13"])
        else:
            context = browser.new_context()
        page = context.new_page()
        page.on("console", lambda msg: console_errors.append(msg.text) if msg.type == "error" else None)

        # No apiBase query param: the frontend's CSP is connect-src 'self', so
        # it must be loaded same-origin and go through Caddy's /api reverse
        # proxy to the API, not call api_base cross-origin directly.
        url = ui_base if mode == "desktop" else f"{ui_base}/m/"
        print(f"[driver] nav {url}")
        page.goto(url, wait_until="domcontentloaded")

        page.wait_for_selector("#login-email", timeout=15_000)
        page.fill("#login-email", DEMO_EMAIL)
        page.fill("#login-password", DEMO_PASSWORD)
        page.click("#login-submit")

        page.wait_for_selector("text=Demo Project", timeout=15_000)
        shot1 = out_dir / f"{mode}-dashboard.png"
        page.screenshot(path=str(shot1))
        print(f"[driver] screenshot -> {shot1}")

        if mode == "desktop":
            page.click("#sidebar-nav .sidebar-item:has-text('Demo Project')")
            page.wait_for_url("**/board", timeout=15_000)
            page.wait_for_selector("text=Main Board", timeout=15_000)
        else:
            # No direct "board" tab in the bottom nav — drill in via Projects.
            page.click(".nav-item[data-a0='/projects']")
            page.wait_for_url("**/projects", timeout=15_000)
            page.click(".row-card:has-text('Demo Project')")
            page.wait_for_url("**/board", timeout=15_000)
            page.wait_for_timeout(500)

        shot2 = out_dir / f"{mode}-board.png"
        page.screenshot(path=str(shot2))
        print(f"[driver] screenshot -> {shot2}")

        context.close()
        browser.close()

    if console_errors:
        print(f"[driver] {len(console_errors)} console error(s):")
        for e in console_errors:
            print(f"  - {e}")
        return 1

    print("[driver] no console errors")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("mode", choices=["desktop", "mobile"])
    ap.add_argument("--ui-base", default="http://127.0.0.1:8081")
    ap.add_argument("--out", default="/tmp/octbase-driver-shots")
    args = ap.parse_args()
    return run_flow(args.mode, args.ui_base, Path(args.out))


if __name__ == "__main__":
    sys.exit(main())
