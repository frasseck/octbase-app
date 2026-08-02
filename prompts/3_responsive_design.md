You are a senior frontend engineer, product designer, responsive design specialist, accessibility reviewer, and UX polish expert.

I want you to refine the layout of this Jira-like web-based task management MVP.

The product already looks acceptable in a desktop web browser, but smaller layout optimizations may still be possible. The main missing area is responsive design. Every view must work well and be viewable on common smart devices, including phones, tablets, small laptops, and desktop screens.

Product context:
This is a web-based Jira-like task management MVP with projects, boards, tasks, task details, task creation/editing, filtering/searching, statuses/workflows, and related task management functionality.

Your mission:
Audit and improve the layout, responsiveness, visual consistency, spacing, and mobile/tablet usability of the entire application without redesigning the product from scratch.

Primary goals:

1. Make every view responsive.
2. Ensure all pages are usable on phones, tablets, laptops, and desktops.
3. Preserve the existing visual direction unless there is a clear usability problem.
4. Improve spacing, alignment, hierarchy, and component consistency.
5. Fix layout overflow, cramped screens, broken grids, hidden actions, and unusable mobile flows.
6. Keep the MVP clean, simple, and production-ready.
7. Avoid unnecessary rewrites or visual overengineering.

Important:
The app already looks okay on desktop. Do not perform a full redesign. Focus on practical layout refinement and responsive behavior.

Before changing code, inspect:

* All routes/pages/views
* Project overview/dashboard
* Board view
* Task list/table view if present
* Task detail view or modal
* Task creation and editing flows
* Filters/search/sorting controls
* Navigation/sidebar/header
* Settings or project configuration screens if present
* Forms
* Modals, drawers, dropdowns, popovers
* Empty/loading/error states
* Shared UI components
* Styling system, design tokens, Tailwind/CSS/theme files
* Existing responsive breakpoints
* Existing tests and visual/component test setup if available

Start with a responsive layout audit.

Review each major viewport:

* Small phone: 320px-375px
* Standard phone: 390px-430px
* Large phone: 430px-480px
* Small tablet: 600px-768px
* Tablet: 768px-1024px
* Small laptop: 1024px-1280px
* Desktop: 1280px+

For each viewport, check:

* Horizontal overflow
* Elements cut off or hidden
* Text wrapping badly
* Buttons too small or too close together
* Forms too wide or too cramped
* Modals too large for the screen
* Tables unusable on mobile
* Board columns unusable on mobile
* Navigation taking too much space
* Actions hidden or difficult to reach
* Filters/search controls wrapping poorly
* Task cards too dense or too sparse
* Empty/error/loading states fitting correctly
* Touch target size
* Keyboard accessibility
* Focus states
* Visual hierarchy
* Consistent spacing

Responsive design requirements:

* No page should require horizontal scrolling unless it is an intentional board/table pattern.
* Intentional horizontal scrolling must be obvious, smooth, and usable on touch devices.
* Navigation should adapt cleanly on smaller screens.
* Sidebar navigation should collapse, become a drawer, or convert to a mobile-friendly pattern.
* Primary actions must remain visible and easy to access.
* Task creation must be usable on mobile.
* Task editing must be usable on mobile.
* Task details must be readable on mobile.
* Board columns must be usable on mobile, either through horizontal scroll, stacked layout, tabs, or another practical MVP-friendly pattern.
* Filters and search must not dominate small screens.
* Tables should become cards, scroll containers, simplified lists, or responsive layouts where appropriate.
* Modals should become full-screen dialogs or bottom sheets on small screens if needed.
* Forms should use single-column layouts on smaller screens.
* Buttons and controls must have touch-friendly sizing.
* Text should remain readable without excessive truncation.
* Important metadata such as status, priority, due date, and assignee should remain visible where needed.
* Empty states should remain helpful on all screen sizes.

Layout refinement requirements:

* Improve spacing consistency.
* Normalize page padding across breakpoints.
* Align headers, toolbars, cards, forms, and content areas.
* Reduce visual clutter where screens feel cramped.
* Improve visual hierarchy using spacing, typography, and grouping.
* Ensure similar screens use similar layout patterns.
* Avoid one-off layout hacks.
* Reuse shared layout primitives where possible.
* Keep CSS/Tailwind classes readable and maintainable.
* Prefer simple responsive utilities over complex custom CSS unless necessary.

Accessibility requirements:

* Ensure interactive elements are reachable by keyboard.
* Ensure focus states are visible.
* Ensure icon-only buttons have accessible labels.
* Ensure modals/drawers are accessible.
* Ensure touch targets are reasonably sized.
* Do not rely on hover-only interactions for important actions.
* Do not rely on color alone for task status or priority.
* Preserve readable contrast.

Implementation approach:

1. Inspect the app and identify all major layout issues.
2. Produce a concise responsive layout audit.
3. Prioritize issues by user impact.
4. Implement fixes incrementally.
5. Prefer shared layout improvements over repeated page-specific patches.
6. Run relevant checks after changes.
7. Verify the core flows across multiple viewport sizes.

Do not add unnecessary dependencies.
Do not rewrite the entire UI.
Do not change business logic unless required to fix a layout-related bug.
Do not remove existing functionality.
Do not introduce a second competing design system.

Prioritize these flows:

1. User lands in the app and understands navigation.
2. User creates/selects a project.
3. User views a board on mobile.
4. User creates a task on mobile.
5. User opens and edits task details on mobile.
6. User changes task status on mobile.
7. User searches/filters tasks on mobile.
8. User handles empty/loading/error states on mobile.
9. User uses the app on tablet.
10. User uses the app on desktop without regressions.

Suggested responsive patterns:

* Use a collapsible sidebar or mobile drawer for navigation.
* Use responsive page padding: compact on mobile, wider on desktop.
* Use stacked toolbars on mobile and horizontal toolbars on desktop.
* Use card-based task lists on mobile.
* Use horizontal scrolling board columns only when it remains usable.
* Use full-screen task detail modals/drawers on mobile.
* Use multi-column layouts only from tablet or desktop breakpoints upward.
* Use compact metadata badges on small screens.
* Hide secondary actions behind accessible menus on mobile, but keep primary actions visible.
* Use sticky mobile headers or action bars only when they improve usability.

Testing and verification:
Add or update tests where the project supports them.

Check:

* Core pages render without crashes.
* Navigation works on mobile layout.
* Task creation works on mobile layout.
* Task editing works on mobile layout.
* Board/list views remain usable.
* Modals/drawers open and close correctly.
* No obvious horizontal overflow.
* Existing tests still pass.

Run available commands:

* lint
* typecheck
* test
* build
* format check if available

If visual regression tooling exists, use it.
If browser/device testing tools exist, use them.
If automated responsive testing is not available, document manual viewport verification.

Final report:
After implementation, provide:

1. Responsive layout audit summary
2. Major issues found
3. Changes made
4. Views improved
5. Breakpoints handled
6. Tests added or updated
7. Commands run and results
8. Remaining layout limitations
9. Suggested future polish
10. Final responsive readiness verdict

Acceptance criteria:
The task is complete only when:

* All major views are usable on phone, tablet, laptop, and desktop.
* No critical layout overflow remains.
* Navigation works on small screens.
* Board/task workflows work on small screens.
* Forms and dialogs are usable on small screens.
* Primary actions remain accessible.
* Desktop layout is not degraded.
* Styling is more consistent than before.
* Tests/build/lint/typecheck pass where available.
