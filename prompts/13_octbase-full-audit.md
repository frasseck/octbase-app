You are a senior principal engineer, product architect, UX reviewer, QA lead, and clean architecture auditor.

You are reviewing a Jira-like web-based task management MVP that has already gone through multiple AI-assisted improvement passes, including prompts focused on:
- Clean code
- TDD
- Clean architecture
- Consistency
- Usability
- UX refinement
- Frontend polish
- MVP readiness

Your task is to review the whole project again from end to end and bring it to a stable, clean, feature-complete MVP state.

Do not assume previous AI-generated changes were correct.
Verify everything yourself by inspecting the actual repository.

The product is a Jira-like web-based task management tool. The MVP should feel coherent, usable, reliable, and technically maintainable.

Core product expectations:
- Users can create and manage projects.
- Users can create, edit, view, and delete tasks.
- Users can move tasks through statuses or board columns.
- Users can assign tasks if assignees/users exist.
- Users can set priority, due dates, labels, descriptions, or equivalent metadata if already part of the product scope.
- Users can filter, search, sort, or otherwise find tasks efficiently.
- Users can understand the current project, board, task state, and next actions.
- Users receive clear feedback for loading, empty, saving, success, and error states.
- The app is usable by first-time users and efficient enough for returning users.
- The MVP should not feel like a collection of disconnected AI-generated screens.

Your mission:
Review the entire codebase, architecture, tests, UX, UI consistency, product flow, and feature completeness. Then iteratively improve the project until it is stable, clean, and MVP-ready.

Important operating principle:
Repeat the review → plan → implement → test → verify cycle until no critical or high-impact issues remain.

Do not stop after one pass if serious problems remain.
Continue until the project reaches a stable MVP quality bar.

Use this loop:

1. Inspect
2. Audit
3. Prioritize
4. Plan
5. Implement
6. Test
7. Verify
8. Document remaining issues
9. Repeat if needed

Before changing code, inspect:
- Project structure
- Architecture and dependency boundaries
- Domain model
- Use cases / services
- API or backend routes (completeness, consistency, contracts)
- Frontend routes and screens
- Components and design system
- State management
- Data fetching
- Validation
- Error handling
- Authentication / permissions if present
- Database/schema/migrations if present
- Tests
- Build/lint/typecheck setup
- Previous AI-generated inconsistencies
- README or developer documentation

Produce a short initial audit with:
1. Current architecture summary
2. Main product flows
3. API completeness: which frontend features lack a backend endpoint, which endpoints are orphaned or inconsistent
4. Existing test coverage: which layers and flows are covered, which are not
5. Top technical risks
6. Top UX/product risks
7. Missing or incomplete MVP features
8. Areas that look overengineered
9. Areas that look underengineered
10. Inconsistent naming, foldering, styling, or patterns
11. Proposed stabilization plan

Then begin implementation.

MVP quality bar:

The project is only ready when:

Technical quality:
- The app builds successfully.
- Tests pass.
- Type checks pass if available.
- Linting passes if available.
- No obvious runtime errors remain in core flows.
- No obvious dead code, duplicate logic, or conflicting patterns remain in touched areas.
- Architecture boundaries are respected.
- Business logic is not randomly scattered across UI/controllers.
- Naming is clear and consistent.
- Error handling is predictable.
- The code is simple enough for another developer or AI agent to extend safely.

Clean architecture:
- Domain/business rules are separated from infrastructure and presentation.
- Use cases/application services are explicit where useful.
- UI does not contain complex business rules.
- Database/API/framework-specific code does not leak into domain logic.
- Dependencies point in the correct direction.
- Repositories/adapters/services are named and organized consistently.
- Validation and state transitions live in appropriate layers.
- Circular dependencies are removed.
- Large mixed-responsibility files are split only when it improves clarity.

API completeness:
- Every frontend feature has a corresponding backend endpoint.
- No frontend feature silently fails because an endpoint is missing or incomplete.
- No orphaned endpoints exist that no frontend feature calls.
- HTTP methods and status codes are correct and consistent (200, 201, 400, 401, 403, 404, 422, 500).
- Error responses have a consistent structure across all endpoints.
- Request and response shapes match what the frontend actually sends and expects.
- Auth/permission checks are applied consistently across all protected endpoints.
- Filtering, sorting, and pagination parameters are consistent across list endpoints.
- All endpoints are covered by at least one happy-path and one error-path integration test.

Clean code:
- Prefer boring, readable, maintainable code.
- Remove unnecessary abstraction.
- Add abstraction only where it protects boundaries or reduces duplication.
- Keep functions/components focused.
- Use clear names.
- Avoid vague names like data, item, manager, utils, handler unless context makes them clear.
- Remove unused imports, unused files, dead branches, and duplicate helpers.
- Normalize file naming, component naming, DTO naming, hook naming, service naming, and test naming.
- Keep similar features structurally similar.

TDD and testing:
- Before refactoring risky existing behavior, add characterization tests.
- For new or changed behavior, write or update tests first where practical.
- Prioritize tests for:
  - Task creation
  - Task editing
  - Task deletion
  - Task status changes
  - Project/board workflows
  - Filtering/search/sorting
  - Validation
  - Error states
  - Empty states
  - Repository/API boundaries
  - Core domain rules
- Prefer unit tests for pure business logic.
- Prefer integration tests for API/database boundaries.
- Prefer UI or component tests for critical user flows.
- Avoid brittle tests tied to implementation details.
- Test names should describe behavior clearly.

Full test coverage:
- Every exported domain function and service method has at least one unit test.
- Every repository method has at least one integration test against a real database.
- Every API endpoint has at least one happy-path and one error-path integration test.
- Every critical frontend flow has at least one UI or E2E test.
- Validation logic is tested for both valid and invalid inputs.
- Auth-protected endpoints are tested for both authorized and unauthorized access.
- Identify and document any coverage gaps that remain after the audit; classify as MVP blocker or acceptable debt.
- Do not rely solely on passing tests to infer coverage — actively check which behaviors lack tests.

UX and usability:
- Primary actions must be obvious.
- Task creation must be fast and clear.
- Task editing must be understandable.
- Board/list views must be easy to scan.
- Status transitions must be easy and safe.
- Task metadata must be visible where users need it.
- Empty states should guide users toward the next action.
- Loading states should not feel broken.
- Errors should be human-readable and actionable.
- Destructive actions should be safe through confirmation, undo, or clear feedback.
- Navigation should always preserve project/task context.
- The user should never wonder: “Where am I?”, “What happened?”, or “What should I do next?”

UI consistency:
- Standardize buttons, inputs, modals, cards, task items, badges, dropdowns, tables, tabs, and forms.
- Use consistent spacing, typography, icon style, colors, and interaction states.
- Avoid one-off components unless clearly justified.
- Reuse existing design primitives where possible.
- Fix visual inconsistencies caused by AI-generated code.
- Make the MVP feel like one product, not multiple prototypes.

Accessibility:
- Use semantic HTML where applicable.
- Ensure interactive elements are keyboard-accessible.
- Ensure focus states are visible.
- Ensure forms have labels.
- Ensure modals/dropdowns are accessible.
- Do not rely on color alone for task status or priority.
- Check contrast where obvious.
- Add accessible names to icon-only buttons.

Feature completeness:
Evaluate whether the existing MVP scope is complete.

Do not add large speculative features.
Do add or finish small missing pieces that are necessary for the current product to work coherently.

Classify every missing feature as:
- Critical for MVP
- Important but can wait
- Nice to have
- Out of scope

Prioritize critical MVP gaps only.

Core flows to verify manually or with tests:
1. First user lands in the app and understands what to do.
2. User creates or selects a project.
3. User creates a task.
4. User views task details.
5. User edits task fields.
6. User changes task status.
7. User searches/filters tasks.
8. User handles an empty project/board.
9. User sees helpful feedback when something fails.
10. User can return to the board/project without losing context.

Implementation rules:
- Work in small safe patches.
- Do not rewrite the whole app unless the current structure is impossible to stabilize.
- Preserve existing behavior unless clearly broken.
- Fix bugs when found and document the fix.
- Do not introduce unnecessary dependencies.
- Do not add large features just because they are common in Jira.
- Keep MVP scope focused.
- Prefer incremental stabilization over dramatic redesign.
- After each meaningful change, run the relevant checks.
- When checks fail, fix the root cause rather than hiding the failure.
- Do not mark the project complete while build, tests, linting, or type checks are broken.

Commands:
First discover the correct commands from package files, README, CI config, or project scripts.

Run whichever are available:
- install/dependency check
- lint
- typecheck
- test
- build
- database migration check if applicable
- format check if applicable

If a command is unavailable, document that clearly.
If a command fails because of pre-existing unrelated issues, document it and decide whether it blocks MVP readiness.

During each iteration, produce:

Iteration report:
1. What you inspected
2. Problems found
3. Changes made
4. Tests added or updated
5. Commands run
6. Results
7. Remaining blockers
8. Next iteration focus

Repeat the iteration if:
- Any critical MVP flow is broken.
- Build fails.
- Tests fail.
- Type checks fail.
- Lint fails because of relevant project code.
- Major architectural violations remain in core touched areas.
- The UI has obvious inconsistency in core screens.
- Task/project workflows are confusing or incomplete.
- Important error/empty/loading states are missing.
- There are still obvious AI-generated inconsistencies.
- A frontend feature has no corresponding backend endpoint.
- An API endpoint has no test coverage.
- A domain rule or service method has no unit test.
- Auth checks are missing or inconsistent on protected endpoints.

Stop only when:
- No critical blockers remain.
- No high-impact usability issues remain in the core MVP flow.
- Core task/project workflows work end to end.
- The codebase is cleaner and more consistent than when you started.
- Tests/build/typecheck/lint pass where available.
- All API endpoints have test coverage.
- All domain/service logic has unit test coverage.
- No frontend feature is missing a backend endpoint.
- Remaining coverage gaps are documented and classified as acceptable post-MVP debt.
- Remaining issues are documented and are not MVP blockers.

Final deliverable:
When the project is stable, provide a final MVP readiness report:

1. Executive summary
2. What was improved
3. Architecture status
4. API completeness status: list of endpoints with coverage, list of any gaps
5. UX/usability status
6. Test coverage status: by layer (domain, service, API, frontend), noting any remaining gaps
7. Commands run and final results
8. Remaining technical debt
9. Remaining product limitations
10. Recommended post-MVP improvements
11. Final verdict:
   - MVP ready
   - MVP ready with minor caveats
   - Not MVP ready

Be strict but practical.
The goal is not perfection.
The goal is a stable, coherent, clean, usable MVP that can be confidently demoed, tested by users, and extended after launch.