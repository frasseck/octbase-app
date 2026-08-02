> **Superseded:** i18n has already been implemented; see the `i18n` skill for
> the current, canonical how-to. Note the locale set below is stale — French
> was removed and the app now ships **English and German only**; do not use
> `fr.json` as a template for adding a language.

You are a senior full-stack engineer specializing in internationalization, localization, frontend architecture, and Go API design.

Context:
I have a web-based task-management application. The frontend and backend are separated:
- Frontend: HTML and JavaScript
- Backend: Go API

Goal:
Make the application fully multi-language ready using standardized language files. The app should support adding new languages without changing application logic.

Target outcome:
- All user-facing text must be moved out of hardcoded HTML, JavaScript, and Go code.
- Text must be managed through standardized translation files.
- The frontend must load and display the correct language.
- The backend must return language-neutral data where possible and localized messages only where necessary.
- Adding a new language should require adding a new language file, not rewriting code.

Preferred translation structure:
Use one translation file per language, for example:

/locales
  /en.json
  /de.json
  /fr.json

Each file should use stable, namespaced translation keys, for example:

{
  "app": {
    "title": "Task Manager"
  },
  "nav": {
    "tasks": "Tasks",
    "projects": "Projects",
    "settings": "Settings"
  },
  "task": {
    "create": "Create task",
    "edit": "Edit task",
    "delete": "Delete task",
    "complete": "Mark as complete",
    "reopen": "Reopen task",
    "emptyState": "No tasks yet"
  },
  "form": {
    "save": "Save",
    "cancel": "Cancel",
    "required": "This field is required"
  },
  "errors": {
    "generic": "Something went wrong",
    "network": "Network error. Please try again.",
    "unauthorized": "Your session has expired. Please sign in again."
  }
}

Tasks:

1. Audit the existing codebase
   - Find all hardcoded user-facing strings in HTML, JavaScript, and Go.
   - Include button labels, headings, placeholders, aria-labels, validation messages, error messages, toast messages, modal text, empty states, tooltips, confirmation dialogs, email-like text, and status labels.
   - Separate user-facing strings from internal logs, developer errors, and API-only constants.
   - Produce a table with:
     - current text
     - file location
     - proposed translation key
     - target namespace
     - notes

2. Define an i18n architecture
   - Recommend a simple, maintainable i18n approach for plain HTML and JavaScript.
   - Use standardized JSON language files.
   - Define how translations are loaded.
   - Define fallback language behavior.
   - Define how missing translations are handled.
   - Define how the selected language is stored, for example localStorage, cookie, or user profile setting.
   - Define how the document lang attribute is updated.
   - Define how language switching works without a full app rewrite.

3. Refactor the frontend
   - Replace hardcoded HTML text with translation keys.
   - Replace hardcoded JavaScript strings with translation lookups.
   - Add a translation helper function, for example t("task.create").
   - Support nested translation keys.
   - Support fallback values.
   - Support variable interpolation, for example:
     - "Task {{title}} was created"
     - "{{count}} tasks selected"
   - Support pluralization where needed.
   - Ensure translated text also works for:
     - aria-label
     - aria-describedby
     - title attributes
     - placeholders
     - validation messages
     - toast messages
     - modal dialogs
     - confirmation prompts
     - dynamically rendered task items

4. Refactor the backend Go API
   - Identify all user-facing messages currently returned by the API.
   - Decide which messages should be returned as:
     - stable machine-readable error codes
     - field names
     - translation keys
     - localized fallback messages
   - Prefer returning structured error codes rather than hardcoded localized text.
   - Design a consistent API error response format, for example:

     {
       "error": {
         "code": "TASK_TITLE_REQUIRED",
         "messageKey": "errors.taskTitleRequired",
         "field": "title",
         "details": {}
       }
     }

   - Ensure the frontend can map backend errors to localized messages.
   - Keep logs and internal errors in a developer-friendly language.
   - Avoid mixing business logic with localization logic unless absolutely necessary.

5. Design the translation key system
   - Use stable semantic keys, not text-based keys.
   - Organize keys by domain:
     - app
     - nav
     - auth
     - task
     - project
     - form
     - validation
     - errors
     - notifications
     - accessibility
     - dates
     - settings
   - Avoid duplicate keys for the same concept.
   - Avoid overly generic keys that become ambiguous.
   - Provide a recommended naming convention.

6. Date, time, number, and locale formatting
   - Replace hardcoded date and number formatting.
   - Use locale-aware formatting in the frontend.
   - Ensure task due dates, created dates, updated dates, and relative dates are locale-aware.
   - Keep backend timestamps in a standard machine-readable format such as ISO 8601.
   - Let the frontend format dates according to the selected locale.

7. Language selector
   - Add or improve a language selector.
   - The selector must:
     - show available languages
     - persist the selected language
     - update visible UI text
     - update the html lang attribute
     - be keyboard accessible
     - have accessible labels
   - Provide code for the language selector.

8. Accessibility requirements
   - Translated text must also apply to accessibility-related strings.
   - Include translation keys for:
     - aria-labels
     - screen reader only text
     - status updates
     - form error messages
     - modal labels
     - icon button labels
   - Ensure language switching does not break WCAG AA accessibility work.
   - Ensure the page has the correct lang attribute for the active language.

9. Testing
   - Add tests or test examples for:
     - loading default language
     - switching language
     - missing translation fallback
     - variable interpolation
     - pluralization
     - backend error-code mapping
     - translated form validation messages
     - translated dynamic task updates
   - Add a check that prevents new hardcoded user-facing strings from being introduced where possible.

10. Deliverables
   Provide the final answer in this structure:

   1. Summary
   2. Current hardcoded text audit
   3. Recommended i18n architecture
   4. Proposed folder structure
   5. Translation file examples
   6. Frontend implementation
   7. Backend Go API changes
   8. Error-handling strategy
   9. Date, time, and number localization
   10. Language selector implementation
   11. Testing strategy
   12. Migration checklist
   13. Open questions or assumptions

Important implementation rules:
- Do not hardcode user-facing strings in HTML, JavaScript, or Go handlers.
- Use translation keys everywhere user-facing text appears.
- Keep translation keys stable even if the visible text changes.
- Do not translate database enum values directly; map stable values to translation keys in the frontend.
- Do not localize API field names.
- Keep API responses predictable and language-neutral where possible.
- Make the system simple enough to maintain without a heavy framework unless one is already used.
- Provide concrete code examples, not only conceptual advice.
