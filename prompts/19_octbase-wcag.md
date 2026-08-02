You are a senior accessibility engineer, WCAG auditor, and full-stack developer with experience in HTML, JavaScript, and Go APIs.

Goal:
Bring my web application, a task-management system, up to WCAG 2.2 Level AA. Frontend and backend are separate:
- Frontend: HTML and JavaScript
- Backend: Go API

Approach:
1. First, analyze the existing application in a structured way.
2. Then create a concrete action plan.
3. Only change code once it's clear which barriers exist and which files are affected.
4. Prioritize real usability over purely formal ARIA usage.
5. Use native HTML elements wherever possible instead of unnecessary ARIA workarounds.
6. Briefly explain each change with reference to the WCAG issue it addresses.
7. Clearly separate frontend fixes, backend fixes, tests, and manual checks.

In the frontend, check especially:

- Semantic HTML structure:
  - correct landmarks such as header, nav, main, aside, footer
  - sensible heading hierarchy
  - buttons as button, links as a
  - lists, tables, and forms that are semantically correct

- Keyboard operability:
  - all functions fully operable via keyboard
  - visible focus state
  - no keyboard traps
  - logical tab order
  - skip link to main content
  - dropdowns, dialogs, menus, filters, sorting, and task actions usable via keyboard

- Task-management-specific functions:
  - creating, editing, deleting, completing, and reopening tasks
  - task status understandable to screen readers
  - drag-and-drop only with an accessible alternative
  - priorities, labels, deadlines, and states not conveyed by color alone
  - filters, search, and sorting with accessible labels
  - empty states, loading states, and error messages output accessibly
  - notifications, toasts, and status changes use appropriate live regions

- Forms:
  - every input field has a visible label
  - clear error messages
  - errors programmatically associated with fields
  - required fields clearly marked
  - no error communication via color alone
  - focus management after validation errors

- Modal dialogs and overlays:
  - focus moves into the dialog on open
  - focus stays within the dialog
  - dialog can be closed with Escape, where appropriate
  - focus is reasonably restored after closing
  - accessible name and description set

- Visual accessibility:
  - sufficient color contrast for text, icons, buttons, inputs, and states
  - visible focus indicators
  - layout works with zoom and text enlargement
  - no information conveyed solely through color, position, or symbols
  - responsive reflow without horizontal scrolling at typical breakpoints

- ARIA:
  - use ARIA only when native HTML semantics are insufficient
  - use aria-label, aria-labelledby, and aria-describedby correctly
  - no conflicting roles or incorrect aria-* attributes
  - status changes announced sensibly via aria-live
  - interactive components follow the correct role/state/property model

In the backend, i.e. the Go API, check especially:

- Validation errors:
  - return structured, field-related error messages
  - error messages must be unambiguously assignable to individual fields by the frontend
  - provide understandable text instead of just error codes

- API design:
  - consistent HTTP status codes
  - consistent JSON error structure
  - stable IDs for tasks, labels, projects, and users
  - design pagination, filtering, and sorting so the frontend can present them accessibly
  - do not force API workflows that are only sensibly usable via mouse interaction

- Data model:
  - provide task status, priority, and categories as text values, not only as colors or numeric codes
  - provide date values unambiguously and machine-readably
  - clearly document optional fields
  - server-side validation must match the frontend rules

- Security and session behavior:
  - return session expiry and authentication errors so the frontend can display accessible hints
  - no sudden redirects without an explainable state
  - error messages may be understandable without exposing sensitive information

Produce the following deliverables:

1. Accessibility audit
   - table with issue, affected location, WCAG reference, severity, and recommended fix
   - severity levels: critical, high, medium, low
   - separate automatically detected findings from items requiring manual review

2. Implementation plan
   - prioritized order of fixes
   - quick wins
   - structural refactorings
   - risks or breaking changes

3. Frontend code changes
   - concrete changes in HTML and JavaScript
   - accessible component patterns for:
     - buttons and icon buttons
     - forms
     - error messages
     - task lists
     - modal dialogs
     - toasts and status messages
     - filters, search, and sorting
     - drag-and-drop alternatives

4. Backend code changes in Go
   - consistent error responses
   - validation structure
   - example accessible API response formats
   - adjustments to DTOs, handlers, and tests as needed

5. Tests
   - automated accessibility tests for the frontend
   - keyboard navigation tests
   - form and error message tests
   - API tests for validation errors
   - regression tests for critical task flows

Recommended test tools, where appropriate:
- axe-core or comparable accessibility checks
- Playwright for keyboard and end-to-end tests
- HTML validator
- Lighthouse only as a supplement, not as sole evidence
- manual tests with keyboard and screen reader

Also define a manual test checklist for these core flows:

- Login
- Create a task
- Edit a task
- Complete a task
- Delete a task
- Search for a task
- Filter tasks
- Sort tasks
- Switch project or board
- Trigger and understand an error message
- Experience a session expiry or API error

Acceptance criteria:

- All core functions are operable without a mouse.
- Focus is always visible and logical.
- Screen readers receive meaningful names, roles, states, and status messages.
- Forms have visible labels and clear, field-related error messages.
- No information is conveyed solely through color.
- Modal dialogs, menus, and dynamic task updates are accessible.
- Backend errors can be displayed unambiguously and accessibly in the frontend.
- Automated tests cover key accessibility regression risks.
- In addition to automated checks, there is a documented manual WCAG AA review.

Provide your answer in this structure:

1. Brief summary
2. Issues found
3. Prioritized actions
4. Concrete code changes
5. Backend adjustments
6. Test strategy
7. Manual WCAG AA checklist
8. Open questions or assumptions

Important:
When you propose code, provide concrete, directly usable examples. When you analyze existing files, name the affected files and functions. Avoid generic recommendations without concrete implementation.
