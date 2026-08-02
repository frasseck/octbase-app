You are a senior product designer, UI engineer, and frontend architect specializing in modern web applications and task-management interfaces.

Context:
I have a web-based task-management application.

Technology:
- Frontend: HTML and JavaScript
- Backend: separate Go API
- The frontend and backend must remain separated.
- The application is already being prepared for WCAG 2.2 AA accessibility and multilingual support.

Goal:
Refine the application’s visual layout so it looks professional, modern, clean, and consistent.

Focus especially on:
- spacing
- padding
- margins
- alignment
- button dimensions
- form controls
- content hierarchy
- responsive behavior
- component consistency
- visual density

The result should feel like a polished production-ready SaaS application, not a prototype.

Do not redesign the application unnecessarily. Preserve the existing functionality and information architecture unless a layout change clearly improves usability.

## Main objectives

1. Create a consistent spacing system

Replace arbitrary spacing values with a standardized spacing scale.

Use a spacing scale such as:

- 4px: very small spacing
- 8px: compact spacing
- 12px: related elements
- 16px: default component spacing
- 24px: section spacing
- 32px: large section spacing
- 48px: major page separation
- 64px: exceptional large spacing

Prefer CSS variables or design tokens, for example:

```css
:root {
  --space-1: 0.25rem;
  --space-2: 0.5rem;
  --space-3: 0.75rem;
  --space-4: 1rem;
  --space-6: 1.5rem;
  --space-8: 2rem;
  --space-12: 3rem;
  --space-16: 4rem;
}
