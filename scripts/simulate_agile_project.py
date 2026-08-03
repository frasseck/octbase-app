#!/usr/bin/env python3
"""
simulate_agile_project.py — end-to-end, multi-user scenario test for Octbase.

This exercises the *whole* product the way a real 5-person team would if they
used Octbase to build Octbase itself:

  * Super Admin onboards a 5-person team, each with a distinct global role.
  * A Product Owner spins up the "Octbase" project and staffs it with the four
    project roles (OWNER / ADMIN / MEMBER / VIEWER).
  * The team configures categories, a release and two sprints.
  * They fill a product backlog of epics, stories and tasks (bug fixes and
    chores are plain tasks), assign the work, relate/link/discuss it.
  * They run Sprint 1 through a real board workflow (enroll → In Progress →
    In Review → Done), then complete it and carry unfinished work over.
  * They plan and start Sprint 2, run a release close/reopen, write wiki docs,
    and finally verify search, activity feeds, dashboards, notifications and
    the audit log.
  * RBAC and sprint-scope invariants are asserted along the way (a viewer
    cannot write; a global USER cannot create projects; a running sprint's
    scope is locked).

It talks only to the REST API (no browser), so a full run takes seconds.
It is deterministic against a freshly seeded, demo-mode API and exits
non-zero if any assertion fails.

Usage:
    python3 simulate_agile_project.py [--base http://127.0.0.1:8000]

Environment:
    OCTBASE_API_BASE   overrides --base (default http://127.0.0.1:8000)
"""

from __future__ import annotations

import argparse
import os
import sys
import time
import uuid

import requests

# --------------------------------------------------------------------------- #
# Test harness
# --------------------------------------------------------------------------- #

PASS, FAIL = 0, 0
_RESULTS: list[tuple[bool, str, str]] = []


def section(title: str) -> None:
    print(f"\n\033[1;36m=== {title} ===\033[0m")


def check(name: str, ok: bool, detail: str = "") -> bool:
    """Record and print a single assertion; never raises."""
    global PASS, FAIL
    ok = bool(ok)
    if ok:
        PASS += 1
    else:
        FAIL += 1
    _RESULTS.append((ok, name, detail))
    mark = "\033[32m✓\033[0m" if ok else "\033[31m✗\033[0m"
    line = f"  {mark} {name}"
    if detail and not ok:
        line += f"  \033[31m→ {detail}\033[0m"
    print(line)
    return ok


class Fatal(Exception):
    """A precondition failed so hard that continuing is pointless."""


# --------------------------------------------------------------------------- #
# API client
# --------------------------------------------------------------------------- #


class Client:
    """A logged-in API session for one user (or a raw token)."""

    def __init__(self, base: str, label: str, token: str):
        self.base = base.rstrip("/")
        self.label = label
        self.token = token
        self.s = requests.Session()
        self.s.headers.update(
            {"Content-Type": "application/json", "Authorization": f"Bearer {token}"}
        )

    @classmethod
    def login(cls, base: str, label: str, email: str, password: str) -> "Client":
        r = requests.post(
            f"{base.rstrip('/')}/api/v1/auth/login",
            json={"email": email, "password": password},
            timeout=15,
        )
        if r.status_code != 200:
            raise Fatal(f"login failed for {email}: {r.status_code} {r.text}")
        token = r.json().get("accessToken")
        if not token:
            raise Fatal(f"login for {email} returned 200 with no accessToken: {r.text}")
        return cls(base, label, token)

    # -- low level ---------------------------------------------------------- #
    def raw(self, method: str, path: str, data=None):
        """Return (status_code, parsed_body) without asserting."""
        url = f"{self.base}{path}"
        r = self.s.request(method, url, json=data, timeout=30)
        try:
            body = r.json()
        except ValueError:
            body = r.text
        return r.status_code, body

    def _ok(self, method: str, path: str, data=None, expect=(200, 201)):
        status, body = self.raw(method, path, data)
        if status not in expect:
            raise Fatal(
                f"{self.label} {method} {path} -> {status} {body} "
                f"(expected {expect})"
            )
        return body

    def get(self, path):
        return self._ok("GET", path, expect=(200,))

    def post(self, path, data=None, expect=(200, 201)):
        return self._ok("POST", path, data or {}, expect=expect)

    def patch(self, path, data):
        return self._ok("PATCH", path, data, expect=(200,))

    def delete(self, path):
        return self._ok("DELETE", path, expect=(200, 204))


API = "/api/v1"


def as_list(body):
    """Normalise the handful of list-ish response shapes into a plain list."""
    if isinstance(body, list):
        return body
    if isinstance(body, dict):
        for key in ("items", "tasks", "projects", "notifications", "results", "data", "logs"):
            if isinstance(body.get(key), list):
                return body[key]
    return []


def uniq(prefix: str) -> str:
    return f"{prefix}-{uuid.uuid4().hex[:6]}"


# --------------------------------------------------------------------------- #
# Scenario
# --------------------------------------------------------------------------- #


def run(base: str) -> None:
    start = time.time()

    # ---- Phase 0: preflight ---------------------------------------------- #
    section("Phase 0 · Preflight")
    h = requests.get(f"{base}{API}/health", timeout=10)
    health = h.json()
    check("API health is ok", health.get("status") == "ok", str(health))
    check("DB reports a migration version",
          isinstance(health.get("db", {}).get("migrationVersion"), int), str(health))

    super_ = Client.login(base, "super", "super@octbase.dev", "superpass1234")

    # ---- Phase 1: onboard the team --------------------------------------- #
    section("Phase 1 · Onboard the 5-person team")
    # (label, display name, email, global role, project role, hat)
    team_spec = [
        ("alice", "Alice Anderson", "alice", "ADMIN", "PROJECT_OWNER",  "Product Owner"),
        ("bob",   "Bob Baker",      "bob",   "USER",  "PROJECT_ADMIN",  "Scrum Master / Tech Lead"),
        ("carol", "Carol Chen",     "carol", "USER",  "PROJECT_MEMBER", "Backend Engineer"),
        ("dave",  "Dave Diaz",      "dave",  "USER",  "PROJECT_MEMBER", "Frontend Engineer"),
        ("erin",  "Erin Evans",     "erin",  "USER",  "PROJECT_MEMBER", "QA Engineer"),
    ]
    # A shared, unique suffix keeps the run idempotent even without a DB reset.
    run_id = uuid.uuid4().hex[:6]
    users: dict[str, dict] = {}
    clients: dict[str, Client] = {}
    for label, name, local, grole, prole, hat in team_spec:
        email = f"{local}+{run_id}@octbase.dev"
        pw = "Passw0rd!42"
        u = super_.post(
            f"{API}/users",
            {"email": email, "displayName": name, "password": pw, "globalRole": grole},
            expect=(201,),
        )
        u["_password"] = pw
        u["_projectRole"] = prole
        u["_hat"] = hat
        users[label] = u
        check(f"created {name} ({grole}, {hat})",
              u.get("id") and u.get("globalRole") == grole, str(u))
        clients[label] = Client.login(base, label, email, pw)

    # Global-role RBAC: a plain USER must not be able to create a project.
    st, body = clients["bob"].raw("POST", f"{API}/projects",
                                  {"name": "Bob's rogue project", "visibility": "PRIVATE"})
    check("global USER is denied project creation (403)", st == 403, f"{st} {body}")

    # Resolve the seeded demo user to play a read-only stakeholder (VIEWER).
    all_users = as_list(super_.get(f"{API}/users"))
    demo = next((u for u in all_users if u.get("email") == "demo@octbase.dev"), None)
    check("seeded demo user is present (stakeholder)", demo is not None)

    # ---- Phase 2: create & staff the project ----------------------------- #
    section("Phase 2 · Create the Octbase project & staff it")
    alice = clients["alice"]
    proj = alice.post(
        f"{API}/projects",
        {
            "name": f"Octbase {run_id}",
            "abbreviation": "OCTB",
            "description": "Agile project-management software, built by its own team.",
            "visibility": "PRIVATE",
        },
        expect=(201,),
    )
    pid = proj.get("id")
    if not pid:
        raise Fatal(f"project create returned 201 but no id: {proj}")
    check("Alice created the project", proj.get("visibility") == "PRIVATE", str(proj))

    # Creator is auto PROJECT_ADMIN; Super Admin promotes her to PROJECT_OWNER.
    super_.patch(f"{API}/projects/{pid}/memberships/{users['alice']['id']}",
                 {"role": "PROJECT_OWNER"})
    # Owner staffs the rest of the team with their project roles.
    for label in ("bob", "carol", "dave", "erin"):
        u = users[label]
        alice.post(f"{API}/projects/{pid}/memberships",
                   {"userId": u["id"], "role": u["_projectRole"]}, expect=(201,))
    if demo:
        alice.post(f"{API}/projects/{pid}/memberships",
                   {"userId": demo["id"], "role": "PROJECT_VIEWER"}, expect=(201,))

    memberships = as_list(alice.get(f"{API}/projects/{pid}/memberships"))
    roles = {m.get("userId"): m.get("role") for m in memberships}
    check("Alice is PROJECT_OWNER", roles.get(users["alice"]["id"]) == "PROJECT_OWNER", str(roles))
    check("Bob is PROJECT_ADMIN", roles.get(users["bob"]["id"]) == "PROJECT_ADMIN", str(roles))
    check("Carol/Dave/Erin are PROJECT_MEMBER",
          all(roles.get(users[l]["id"]) == "PROJECT_MEMBER" for l in ("carol", "dave", "erin")),
          str(roles))
    check("all four project roles are represented",
          {"PROJECT_OWNER", "PROJECT_ADMIN", "PROJECT_MEMBER", "PROJECT_VIEWER"} <= set(roles.values()),
          str(sorted(set(roles.values()))))

    # Project-role RBAC: a member can read; the viewer can read but not write.
    st, _ = clients["carol"].raw("GET", f"{API}/projects/{pid}/tasks")
    check("PROJECT_MEMBER can read the private project (200)", st == 200, str(st))
    if demo:
        viewer = Client.login(base, "viewer", "demo@octbase.dev", "demopass1234")
        st, _ = viewer.raw("GET", f"{API}/projects/{pid}/tasks")
        check("PROJECT_VIEWER can read tasks (200)", st == 200, str(st))
        st, body = viewer.raw("POST", f"{API}/projects/{pid}/tasks",
                              {"title": "viewer should not create this"})
        check("PROJECT_VIEWER is denied task creation (403)", st == 403, f"{st} {body}")

    # ---- Phase 3: configure categories, release, sprints ----------------- #
    section("Phase 3 · Configure categories, a release and two sprints")
    for cname, color in [("Backend", "blue"), ("Frontend", "green"), ("Design", "purple"),
                         ("QA", "orange"), ("DevOps", "red")]:
        alice.post(f"{API}/projects/{pid}/task-categories",
                   {"name": cname, "color": color}, expect=(200, 201))
    cats = as_list(alice.get(f"{API}/projects/{pid}/task-categories"))
    check("5 task categories created", len(cats) >= 5, f"got {len(cats)}")
    # The API is known to answer 200/201 for writes it silently drops — read
    # written fields back instead of trusting the status code (same pattern as
    # the status read-back in Phase 6).
    backend_cat = next((c for c in cats if c.get("name") == "Backend"), {})
    check("category color survived the write (read-back)",
          backend_cat.get("color") == "blue", str(backend_cat))

    release = alice.post(f"{API}/projects/{pid}/releases",
                         {"name": "v1.0 — MVP", "goal": "First shippable release",
                          "dueDate": "2026-09-30"}, expect=(200, 201))
    rid = release["id"]
    check("release v1.0 created (PLANNED)", release.get("status") == "PLANNED", str(release))
    rel_back = alice.get(f"{API}/releases/{rid}")
    check("release goal and dueDate survived the write (read-back)",
          rel_back.get("goal") == "First shippable release"
          and str(rel_back.get("dueDate") or "").startswith("2026-09-30"),
          str({k: rel_back.get(k) for k in ("goal", "dueDate")}))

    bob = clients["bob"]
    sprint1 = bob.post(f"{API}/projects/{pid}/sprints",
                       {"name": "Sprint 1 — Foundations", "goal": "Auth + core task/board CRUD",
                        "startDate": "2026-07-01", "endDate": "2026-07-14", "releaseId": rid},
                       expect=(200, 201))
    sprint2 = bob.post(f"{API}/projects/{pid}/sprints",
                       {"name": "Sprint 2 — Boards & Backlog", "goal": "Sprints, search, backlog",
                        "startDate": "2026-07-15", "endDate": "2026-07-28", "releaseId": rid},
                       expect=(200, 201))
    check("Sprint 1 created (PLANNED)", sprint1.get("status") == "PLANNED", str(sprint1))
    check("Sprint 2 created (PLANNED)", sprint2.get("status") == "PLANNED", str(sprint2))
    s1, s2 = sprint1["id"], sprint2["id"]
    s1_back = bob.get(f"{API}/sprints/{s1}")
    check("sprint releaseId survived the write (read-back)",
          s1_back.get("releaseId") == rid, str(s1_back.get("releaseId")))

    # ---- Phase 4: fill the product backlog ------------------------------- #
    section("Phase 4 · Fill the product backlog")

    def A(label):  # assignee id helper
        return users[label]["id"]

    # key, title, type, priority, assignee, sprint, due
    backlog_spec = [
        ("epic_auth",  "Authentication & Identity",           "EPIC",  "HIGH",     None,    None, None),
        ("epic_board", "Task & Board Management",              "EPIC",  "HIGH",     None,    None, None),
        ("epic_plan",  "Sprints & Releases",                  "EPIC",  "MEDIUM",   None,    None, None),
        ("epic_docs",  "Docs & Search",                       "EPIC",  "MEDIUM",   None,    None, None),
        # Sprint 1 scope
        ("jwt",        "JWT login & refresh endpoints",       "STORY", "HIGH",     "carol", s1,   "2026-07-07"),
        ("invite",     "User invitation email flow",          "STORY", "MEDIUM",   "carol", s1,   "2026-07-11"),
        ("kanban",     "Kanban board with drag & drop",       "STORY", "HIGH",     "dave",  s1,   "2026-07-09"),
        ("taskcrud",   "Task CRUD REST endpoints",            "TASK",  "MEDIUM",   "dave",  s1,   "2026-07-08"),
        ("ci",         "Set up CI pipeline & migrations",     "TASK",  "LOW",      "bob",   s1,   "2026-07-05"),
        ("bug_reorder","Board column off-by-one on reorder",  "TASK",  "CRITICAL", "dave",  s1,   "2026-07-06"),
        # Sprint 2 scope
        ("lifecycle",  "Sprint lifecycle (start/complete)",   "STORY", "HIGH",     "bob",   s2,   "2026-07-20"),
        ("ordering",   "Backlog ordering & filters",          "STORY", "MEDIUM",   "carol", s2,   "2026-07-22"),
        ("search",     "Full-text search over tasks & pages", "STORY", "MEDIUM",   "dave",  s2,   "2026-07-24"),
        # Un-scheduled backlog
        ("oauth",      "OAuth SCM integration",               "STORY", "LOW",      "carol", None, None),
        ("bug_tz",     "Timezone drift in due dates",         "TASK",  "MEDIUM",   "erin",  None, None),
    ]
    tasks: dict[str, dict] = {}
    for key, title, ttype, prio, assignee, sprint, due in backlog_spec:
        payload = {"title": title, "taskType": ttype, "priority": prio,
                   "description": f"<p>{title} — see epic for acceptance criteria.</p>"}
        if assignee:
            payload["assigneeId"] = A(assignee)
        if sprint:
            payload["sprintId"] = sprint
        if due:
            payload["dueDate"] = due
        t = alice.post(f"{API}/projects/{pid}/tasks", payload, expect=(201,))
        tasks[key] = t

    check("all 15 backlog items created", len(tasks) == 15, f"got {len(tasks)}")
    check("every task starts PLANNED",
          all(t["status"] == "PLANNED" for t in tasks.values()))
    no_seq = [k for k, t in tasks.items() if "seqNumber" not in t]
    check("every created task carries a seqNumber", not no_seq, f"missing on {no_seq}")
    seq_numbers = [t["seqNumber"] for t in tasks.values() if "seqNumber" in t]  # in creation order
    check("tasks get monotonic seq numbers",
          seq_numbers == sorted(seq_numbers) and len(set(seq_numbers)) == len(seq_numbers),
          str(seq_numbers))
    check("assignee was honoured on create",
          tasks["jwt"].get("assigneeId") == A("carol"), str(tasks["jwt"].get("assigneeId")))
    check("sprint scope planned on create",
          tasks["jwt"].get("sprintId") == s1, str(tasks["jwt"].get("sprintId")))
    jwt_back = alice.get(f"{API}/tasks/{tasks['jwt']['id']}")
    check("task dueDate survived the write (read-back)",
          str(jwt_back.get("dueDate") or "").startswith("2026-07-07"),
          str(jwt_back.get("dueDate")))
    backlog = as_list(alice.get(f"{API}/projects/{pid}/backlog"))
    check("backlog endpoint lists the unscheduled/unboarded work", len(backlog) >= 15,
          f"got {len(backlog)}")

    # ---- Phase 5: refine — relate, link, discuss ------------------------- #
    section("Phase 5 · Backlog refinement (relations, links, comments)")
    carol, dave = clients["carol"], clients["dave"]

    alice.post(f"{API}/tasks/{tasks['kanban']['id']}/relations",
               {"targetTaskId": tasks["taskcrud"]["id"], "relationType": "RELATES_TO"}, expect=(201,))
    alice.post(f"{API}/tasks/{tasks['ordering']['id']}/relations",
               {"targetTaskId": tasks["kanban"]["id"], "relationType": "BLOCKED_BY"}, expect=(201,))
    alice.post(f"{API}/tasks/{tasks['bug_reorder']['id']}/relations",
               {"targetTaskId": tasks["kanban"]["id"], "relationType": "BLOCKS"}, expect=(201,))
    rels = as_list(alice.get(f"{API}/tasks/{tasks['kanban']['id']}/relations"))
    check("relations recorded on the Kanban story", len(rels) >= 1, str(rels))

    alice.post(f"{API}/tasks/{tasks['kanban']['id']}/links",
               {"url": "https://www.figma.com/file/octbase-board",
                "title": "Board design in Figma"}, expect=(201,))
    links = as_list(alice.get(f"{API}/tasks/{tasks['kanban']['id']}/links"))
    check("external design link attached", len(links) >= 1, str(links))
    check("link title survived the write (read-back)",
          any(l.get("title") == "Board design in Figma" for l in links), str(links))

    c1 = alice.post(f"{API}/tasks/{tasks['jwt']['id']}/comments",
                    {"text": "<p>AC: access + refresh tokens, 15-min access TTL.</p>"}, expect=(201,))
    carol.post(f"{API}/tasks/{tasks['jwt']['id']}/comments",
               {"text": "<p>Should refresh rotate? I'll assume yes.</p>",
                "parentId": c1["id"]}, expect=(201,))
    bob.post(f"{API}/tasks/{tasks['ci']['id']}/comments",
             {"text": "<p>Pipeline will run migrations against a throwaway schema.</p>"}, expect=(201,))
    comments = as_list(alice.get(f"{API}/tasks/{tasks['jwt']['id']}/comments"))
    check("discussion thread captured (with a reply)",
          len(comments) >= 2 and any(c.get("parentId") for c in comments), str(len(comments)))

    # ---- Phase 6: run Sprint 1 through the board ------------------------- #
    section("Phase 6 · Execute Sprint 1 on the board")
    started = bob.post(f"{API}/sprints/{s1}/start", expect=(200,))
    check("Bob started Sprint 1 (ACTIVE)", started.get("status") == "ACTIVE", str(started))

    boards = as_list(bob.get(f"{API}/projects/{pid}/boards"))
    sboard = next((b for b in boards if b.get("isSprintBoard") and b.get("sprintId") == s1), None)
    check("a sprint board was provisioned", sboard is not None, str([b.get("name") for b in boards]))
    if sboard is None:
        raise Fatal("no sprint board; cannot continue Phase 6")
    # The board list omits columns; fetch the full board to get its lanes.
    sboard = bob.get(f"{API}/boards/{sboard['id']}")
    if not isinstance(sboard.get("columns"), list):
        raise Fatal(f"board response carries no columns list: {sboard}")
    col = {c.get("status"): c.get("id") for c in sboard["columns"]}
    check("sprint board copied the workflow columns",
          {"PLANNED", "IN_PROGRESS", "DONE"} <= set(col), str(list(col)))
    # Everything Phase 6 does below indexes these lanes directly; a template
    # change that drops one should die with a diagnosis, not a KeyError.
    needed = {"PLANNED", "IN_PROGRESS", "IN_REVIEW", "DONE"}
    if not needed <= set(col):
        raise Fatal(f"sprint board is missing workflow column(s) "
                    f"{sorted(needed - set(col))} (has {sorted(col)}); cannot run Phase 6")

    sprint1_keys = ["jwt", "invite", "kanban", "taskcrud", "ci", "bug_reorder"]

    def move(actor: Client, key: str, status: str):
        """Enroll/advance a task: place it in the matching board lane and set status."""
        tid = tasks[key]["id"]
        actor.post(f"{API}/boards/{sboard['id']}/move-task",
                   {"taskId": tid, "boardColumnId": col[status], "boardRank": 1000})
        actor.post(f"{API}/tasks/{tid}/status", {"status": status})

    # Enroll the whole committed scope onto the sprint board (planned lane).
    for key in sprint1_keys:
        bob.post(f"{API}/boards/{sboard['id']}/move-task",
                 {"taskId": tasks[key]["id"], "boardColumnId": col["PLANNED"], "boardRank": 1000})
    live = bob.get(f"{API}/sprints/{s1}")
    check("Sprint 1 committed scope = 6 tasks", live.get("committedCount") == 6,
          f"committed={live.get('committedCount')}")

    # A real day of work: devs pull, review, and finish some items.
    move(carol, "jwt", "IN_PROGRESS")
    move(dave, "kanban", "IN_PROGRESS")
    move(dave, "taskcrud", "IN_PROGRESS")
    move(carol, "jwt", "IN_REVIEW")
    move(clients["erin"], "jwt", "DONE")      # QA passes it
    move(dave, "taskcrud", "DONE")
    move(dave, "bug_reorder", "IN_PROGRESS")  # still open at sprint end
    # invite + ci remain PLANNED (won't finish this sprint)

    live = bob.get(f"{API}/sprints/{s1}")
    check("live sprint reports 2 tasks Done", live.get("completedCount") == 2,
          f"completed={live.get('completedCount')}")

    jwt_task = alice.get(f"{API}/tasks/{tasks['jwt']['id']}")
    check("finished story is DONE with a doneAt timestamp",
          jwt_task.get("status") == "DONE" and jwt_task.get("doneAt"), str(jwt_task.get("doneAt")))

    # Sprint-scope invariant: an unscheduled task cannot join a running sprint.
    st, body = bob.raw("POST", f"{API}/boards/{sboard['id']}/move-task",
                       {"taskId": tasks["oauth"]["id"], "boardColumnId": col["PLANNED"], "boardRank": 1})
    check("running sprint scope is locked (422 SPRINT_SCOPE_LOCKED)",
          st == 422 and isinstance(body, dict) and body.get("code") == "SPRINT_SCOPE_LOCKED",
          f"{st} {body}")

    # Complete the sprint: snapshot counts, carry over unfinished work.
    completed = bob.post(f"{API}/sprints/{s1}/complete", expect=(200,))
    check("Sprint 1 completed", completed.get("status") == "COMPLETED", str(completed))
    check("completed sprint snapshots 2/6",
          completed.get("committedCount") == 6 and completed.get("completedCount") == 2,
          f"{completed.get('completedCount')}/{completed.get('committedCount')}")
    carried = alice.get(f"{API}/tasks/{tasks['invite']['id']}")
    check("unfinished work carried back to the backlog (sprint link cleared)",
          carried.get("sprintId") in (None, "") and carried.get("status") != "DONE",
          str(carried.get("sprintId")))

    # ---- Phase 7: plan Sprint 2 & manage the release --------------------- #
    section("Phase 7 · Start Sprint 2 & manage the release")
    started2 = bob.post(f"{API}/sprints/{s2}/start", expect=(200,))
    check("Sprint 2 started (ACTIVE)", started2.get("status") == "ACTIVE", str(started2))
    boards = as_list(bob.get(f"{API}/projects/{pid}/boards"))
    sboard2 = next((b for b in boards if b.get("isSprintBoard") and b.get("sprintId") == s2), None)
    check("Sprint 2 board provisioned", sboard2 is not None)
    if sboard2:
        sboard2 = bob.get(f"{API}/boards/{sboard2['id']}")
        if not isinstance(sboard2.get("columns"), list):
            raise Fatal(f"sprint 2 board response carries no columns list: {sboard2}")
        col2 = {c.get("status"): c.get("id") for c in sboard2["columns"]}
        if not {"PLANNED", "DONE"} <= set(col2):
            raise Fatal(f"sprint 2 board is missing PLANNED/DONE column(s) "
                        f"(has {sorted(col2)}); cannot finish Phase 7")
        for key in ("lifecycle", "ordering", "search"):
            bob.post(f"{API}/boards/{sboard2['id']}/move-task",
                     {"taskId": tasks[key]["id"], "boardColumnId": col2["PLANNED"], "boardRank": 1000})
        bob.post(f"{API}/boards/{sboard2['id']}/move-task",
                 {"taskId": tasks["lifecycle"]["id"], "boardColumnId": col2["DONE"], "boardRank": 1})
        bob.post(f"{API}/tasks/{tasks['lifecycle']['id']}/status", {"status": "DONE"})
        live2 = bob.get(f"{API}/sprints/{s2}")
        check("Sprint 2 shows 3 committed / 1 done",
              live2.get("committedCount") == 3 and live2.get("completedCount") == 1,
              f"{live2.get('completedCount')}/{live2.get('committedCount')}")

    # Only one sprint may be active at a time: a second sprint cannot start
    # while Sprint 2 is running.
    sprint3 = bob.post(f"{API}/projects/{pid}/sprints",
                       {"name": "Sprint 3 — Polish"}, expect=(200, 201))
    st, body = bob.raw("POST", f"{API}/sprints/{sprint3['id']}/start")
    check("a second concurrent sprint is rejected (422 SPRINT_ALREADY_ACTIVE)",
          st == 422 and isinstance(body, dict) and body.get("code") == "SPRINT_ALREADY_ACTIVE",
          f"{st} {body}")

    # Release close / reopen lifecycle.
    closed = alice.post(f"{API}/releases/{rid}/close", expect=(200,))
    check("release can be closed", closed.get("status") == "CLOSED", str(closed))
    reopened = alice.post(f"{API}/releases/{rid}/reopen", expect=(200,))
    check("release can be reopened", reopened.get("status") == "PLANNED", str(reopened))

    # ---- Phase 8: documentation (wiki) ----------------------------------- #
    section("Phase 8 · Team documentation (wiki)")
    handbook = alice.post(f"{API}/projects/{pid}/pages",
                          {"title": "Engineering Handbook",
                           "content": "<h1>Handbook</h1><p>How we build Octbase.</p>"},
                          expect=(200, 201))
    alice.post(f"{API}/pages/{handbook['id']}/publish", {"message": "Initial handbook"})
    dod = bob.post(f"{API}/projects/{pid}/pages",
                   {"title": "Definition of Done",
                    "content": "<ul><li>Reviewed</li><li>Tested</li><li>Documented</li></ul>",
                    "parentPageId": handbook["id"]}, expect=(200, 201))
    bob.post(f"{API}/pages/{dod['id']}/publish", {"message": "DoD v1"})
    carol.post(f"{API}/projects/{pid}/pages",
               {"title": "API Conventions", "content": "<p>Stable error codes, etc.</p>"},
               expect=(200, 201))
    pages = as_list(alice.get(f"{API}/projects/{pid}/pages"))
    published = [p for p in pages if p.get("status") == "PUBLISHED"]
    check("3 wiki pages created", len(pages) >= 3, f"got {len(pages)}")
    check("2 pages published (incl. a child page)", len(published) >= 2, f"got {len(published)}")
    check("child page nested under the handbook",
          any(p.get("parentPageId") == handbook["id"] for p in pages), str(dod.get("parentPageId")))

    # ---- Phase 9: cross-cutting verification ----------------------------- #
    section("Phase 9 · Verify search, activity, dashboards, notifications, audit")
    found = as_list(alice.get(f"{API}/projects/{pid}/search/tasks?q=Kanban"))
    check("task search finds the Kanban story",
          any("Kanban" in (t.get("title") or "") for t in found), f"got {len(found)} hits")

    activity = as_list(alice.get(f"{API}/projects/{pid}/activity"))
    kinds = {a.get("type") or a.get("action") or a.get("kind") for a in activity}
    check("project activity feed is populated", len(activity) >= 10, f"got {len(activity)} events")
    check("activity feed captured sprint + task events",
          any("SPRINT" in str(k) for k in kinds) and any("TASK" in str(k) for k in kinds),
          str(sorted(str(k) for k in kinds)))

    task_activity = as_list(alice.get(f"{API}/tasks/{tasks['jwt']['id']}/activity"))
    check("task history records its lifecycle", len(task_activity) >= 3, f"got {len(task_activity)}")

    dash = carol.get(f"{API}/users/me/dashboard")
    assigned = as_list(dash if isinstance(dash, list) else dash.get("assignedTasks", []))
    check("Carol's dashboard lists her assigned work", len(assigned) >= 1, str(len(assigned)))

    notes = as_list(carol.get(f"{API}/users/me/notifications"))
    check("assignee has notifications", len(notes) >= 1, str(notes))

    audit = as_list(super_.get(f"{API}/audit-logs"))
    audit_actions = {a.get("action") for a in audit}
    check("audit log captured user + membership changes",
          any("USER" in str(a) for a in audit_actions) and
          any("MEMBER" in str(a) for a in audit_actions),
          str(sorted(str(a) for a in audit_actions))[:200])

    final_backlog = as_list(alice.get(f"{API}/projects/{pid}/backlog"))
    check("carried-over + unscheduled work sits back in the backlog",
          any(t["id"] == tasks["invite"]["id"] for t in final_backlog),
          f"backlog size {len(final_backlog)}")

    # ---- Summary --------------------------------------------------------- #
    elapsed = time.time() - start
    section("Summary")
    print(f"  Project: {proj['name']}  ({pid})")
    print(f"  Team:    {', '.join(u['displayName'] for u in users.values())}")
    print(f"  Backlog: {len(tasks)} items across 4 epics, 2 sprints, 1 release, {len(pages)} wiki pages")
    print(f"  Elapsed: {elapsed:.1f}s")
    print(f"\n  \033[1m{PASS} passed, {FAIL} failed\033[0m  ({PASS + FAIL} checks)")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", default=os.environ.get("OCTBASE_API_BASE", "http://127.0.0.1:8000"),
                    help="API base URL (default: %(default)s)")
    args = ap.parse_args()
    try:
        run(args.base)
    except Fatal as e:
        print(f"\n\033[31mFATAL: {e}\033[0m")
        return 2
    except requests.RequestException as e:
        print(f"\n\033[31mFATAL (network): {e}\033[0m")
        return 2
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
