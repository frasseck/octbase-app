# Octbase — Rich Text Editor & File Upload

You are a senior software engineer adding two new features to the Octbase MVP:

1. **Rich text editing** for task descriptions (replacing the plain `<textarea>`).
2. **File upload** for task attachments (replacing the current external-URL
   metadata-only form from prompt 15).

Both features touch the same task panel (`renderTaskDetails` in `app.js`).
Implement them in order; the file upload references the attachment tab built in
prompt 15.

Do not assume previous AI-generated code is correct. Read every relevant file
before changing it. Work in small, safe patches; run `go build && go test` after
every backend change.

---

## Application overview

| Layer | Location |
|---|---|
| Go API | `octbase-api/` — chi router, PostgreSQL, JWT auth |
| Entry point | `cmd/octbase-api/main.go` |
| Frontend | `octbase-frontend/js/app.js` + `css/app.css` (vanilla JS SPA, **no build tool**) |
| Task description | stored in `tasks.description` (TEXT, currently plain text) |
| Attachment metadata | `task_attachments` table — `filename`, `content_type`, `size_bytes`, `external_url` |

The existing task description field is a plain `<textarea id="task-desc">` inside
`renderTaskDetails()` in `app.js`. Saving calls `api.tasks.update(taskId,
{description: value})` which PATCHes `/api/v1/tasks/{taskId}`.

---

## Feature 1 — Rich Text Editor

### Design constraints

- **No build tool, no npm**. The SPA is a single `app.js` file with no bundler.
  Use a CDN-loaded library or write a minimal editor from scratch.
- **Storage format: HTML**. The backend already stores `tasks.description` as
  TEXT with no format enforcement. Store sanitised HTML. The existing plain-text
  descriptions will render as plain text inside the editor — this is acceptable.
- **No server-side render pipeline needed** (unlike the Pages feature which has
  `/pages/{id}/render-preview`). The rich text description is edited and
  displayed entirely in the browser.
- Keep the editor **lightweight** (< 50 KB gzipped). Avoid large frameworks.

### Recommended library: Quill

Load Quill from CDN in `octbase-frontend/index.html` (and
`octbase-api/web/docs.html` does not need to change):

```html
<!-- add to <head> in index.html -->
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/quill@2/dist/quill.snow.css">
<script src="https://cdn.jsdelivr.net/npm/quill@2/dist/quill.js"></script>
```

Quill is MIT-licensed, ~43 KB gzipped, works with no build tool, and produces
clean HTML output. It has a stable v2 API.

**Alternative if Quill is unavailable or unsuitable**: use the browser-native
`contenteditable` + `document.execCommand` approach (supported in all modern
browsers). In that case, implement a minimal toolbar manually (bold, italic,
bullet list, numbered list, heading). The storage and retrieval pattern is the
same either way.

### What to change in `app.js`

#### 1a — Replace the description textarea in `renderTaskDetails`

Current code (in `renderTaskDetails`):
```js
<textarea class="form-input" id="task-desc" rows="4"
  oninput="updateTaskDescriptionDraft('${task.id}',this.value)">${esc(description)}</textarea>
<div class="detail-inline-actions">
  <span class="text-muted text-sm" id="task-desc-status">...</span>
  <button class="btn btn-primary btn-sm" id="task-desc-save" ...>Save</button>
</div>
```

Replace with a Quill container:
```js
<div id="task-desc-editor" class="task-desc-editor"></div>
<div class="detail-inline-actions">
  <span class="text-muted text-sm" id="task-desc-status">
    ${descriptionDirty ? 'Unsaved changes' : 'Saved'}
  </span>
  <button class="btn btn-primary btn-sm" id="task-desc-save"
    ${descriptionDirty ? '' : 'disabled'}
    onclick="saveTaskDescription('${task.id}')">Save</button>
</div>
```

After the panel HTML is injected into the DOM, initialise Quill:
```js
// Call this immediately after setting pane.innerHTML in renderTaskPanel:
initDescriptionEditor(task);
```

#### 1b — `initDescriptionEditor(task)`

```js
function initDescriptionEditor(task) {
  const container = el('#task-desc-editor');
  if (!container || typeof Quill === 'undefined') return;

  const quill = new Quill(container, {
    theme: 'snow',
    placeholder: 'Add a description…',
    modules: {
      toolbar: [
        ['bold', 'italic', 'underline', 'strike'],
        [{ list: 'ordered' }, { list: 'bullet' }],
        [{ header: [1, 2, 3, false] }],
        ['link', 'blockquote', 'code-block'],
        ['clean'],
      ],
    },
  });

  // Set initial content (stored HTML or plain text fallback).
  const draft = getTaskDraft(task);
  quill.clipboard.dangerouslyPasteHTML(draft || '');

  // Track changes: update draft and toggle save button.
  quill.on('text-change', () => {
    const html = quill.getSemanticHTML(); // Quill v2 API
    S.taskDescriptionDrafts[task.id] = html;
    const dirty = html !== (S.taskDescriptionOriginals[task.id] || '');
    const save = el('#task-desc-save');
    const status = el('#task-desc-status');
    if (save) save.disabled = !dirty;
    if (status) status.textContent = dirty ? 'Unsaved changes' : 'Saved';
  });

  // Store reference for saveTaskDescription to read.
  window._activeQuill = quill;
}
```

**Note on Quill v2 API**: `quill.getSemanticHTML()` returns clean HTML.
If using Quill v1 use `quill.root.innerHTML` instead. Check the CDN version.

#### 1c — Update `saveTaskDescription`

```js
async function saveTaskDescription(taskId) {
  // Read from the active Quill instance if present, else fall back to draft.
  const value = window._activeQuill
    ? window._activeQuill.getSemanticHTML()
    : (S.taskDescriptionDrafts[taskId] ?? S.taskDescriptionOriginals[taskId] ?? '');
  try {
    await api.tasks.update(taskId, { description: value });
    S.taskDescriptionOriginals[taskId] = value;
    delete S.taskDescriptionDrafts[taskId];
    window._activeQuill = null;
    toast('Saved', 'success');
    await renderTaskPanel(taskId);
    await renderContent();
  } catch(e) { toast(e.message, 'error'); }
}
```

#### 1d — Display saved HTML in read-only contexts

When a task description is shown outside the editor (e.g., in a future
task-detail full-page view), render the HTML safely using `innerHTML`.
Do **not** use `.innerText` or `esc()` for HTML content — that would
double-encode the markup.

The description is only shown inside the task panel's own editor container, so
no extra read-only rendering is needed for this feature.

#### 1e — `updateTaskDescriptionDraft` and existing draft logic

The existing draft system (`S.taskDescriptionDrafts`, `getTaskDraft`,
`isTaskDraftDirty`) is still used for the save-button state. Keep it. The Quill
`text-change` handler writes to `S.taskDescriptionDrafts[task.id]` just like the
old `oninput` handler did.

### CSS additions

```css
/* Quill editor container inside the task panel */
.task-desc-editor {
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  min-height: 120px;
  font-size: 13px;
}
.task-desc-editor .ql-toolbar {
  border-top: none;
  border-left: none;
  border-right: none;
  border-bottom: 1px solid var(--border);
  border-radius: var(--radius) var(--radius) 0 0;
}
.task-desc-editor .ql-container {
  border: none;
  font-family: inherit;
  font-size: 13px;
}
.task-desc-editor .ql-editor { min-height: 100px; padding: 10px 12px; }
```

### Graceful degradation

If Quill fails to load (offline, CDN blocked), fall back to the plain textarea.
Guard `initDescriptionEditor` with:
```js
if (typeof Quill === 'undefined') {
  // inject a plain textarea with the old oninput handler
  container.innerHTML = `<textarea class="form-input" rows="5"
    oninput="updateTaskDescriptionDraft('${task.id}',this.value)"
    >${esc(getTaskDraft(task))}</textarea>`;
  return;
}
```

### Backend changes

**None.** The description column is already `TEXT`; storing HTML in it is
fine. The `UpdateTask` PATCH handler accepts any string for `description`.

The only validation is the 50,000-character limit in `ValidateTaskInput`
(`workmanagement/domain.go`). HTML is more verbose than plain text, so verify
that a realistic description (a few hundred words with markup) stays well under
50,000 characters. No change needed.

---

## Feature 2 — File Upload for Task Attachments

### Design constraints

- **No dedicated file storage service**. Files are uploaded to the Go API,
  stored on the local filesystem (configurable path), and served back via a
  signed static route.
- Keep it **simple**: a single `multipart/form-data` POST endpoint; no chunked
  upload, no pre-signed URLs, no S3 integration.
- **Max file size**: 10 MB. Enforced by the Go handler.
- **Allowed types**: any (`*/*`). The handler records the browser-reported
  Content-Type. Display is limited to images (shown inline) and other files
  (download link).
- This replaces the external-URL form from the attachments tab in prompt 15
  while keeping the same backend metadata model.

### Backend changes

#### 2a — New environment variable and storage path

Add a storage path config to `cmd/octbase-api/main.go`:
```go
uploadDir := os.Getenv("OCTBASE_UPLOAD_DIR")
if uploadDir == "" {
    uploadDir = "./uploads"
}
if err := os.MkdirAll(uploadDir, 0755); err != nil {
    slog.Error("failed to create upload dir", "error", err)
    os.Exit(1)
}
```

Pass `uploadDir` to the `wmHandler`.

#### 2b — New route

Register in `wmHandler.RegisterRoutes`:
```
POST /api/v1/tasks/{taskId}/attachments/upload
GET  /api/v1/attachments/{filename}          ← serves stored files
```

The existing `POST /api/v1/tasks/{taskId}/attachments` (metadata-only) stays
for backward compatibility.

#### 2c — Upload handler (`workmanagement/attachment_handler.go`)

Create a new file `octbase-api/internal/workmanagement/attachment_handler.go`:

```go
package workmanagement

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"

    "github.com/go-chi/chi/v5"
    "github.com/octbase/octbase-api/internal/shared"
)

const maxUploadBytes = 10 << 20 // 10 MB

func (h *Handler) UploadAttachment(uploadDir string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        taskID := chi.URLParam(r, "taskId")
        t, role, ok := h.taskGuard(w, r, taskID)
        if !ok {
            return
        }
        if err := shared.RequireWriter(role); err != nil {
            shared.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
            return
        }

        r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
        if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
            shared.WriteError(w, http.StatusRequestEntityTooLarge,
                "FILE_TOO_LARGE", "file must be ≤ 10 MB")
            return
        }

        file, header, err := r.FormFile("file")
        if err != nil {
            shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
                "field 'file' is required")
            return
        }
        defer file.Close()

        // Sanitise filename: strip directory traversal, keep extension.
        safeName := filepath.Base(header.Filename)
        if safeName == "" || safeName == "." {
            safeName = "upload"
        }
        // Prefix with UUID to prevent collisions and enumeration.
        storedName := shared.NewUUID() + "_" + safeName
        dst := filepath.Join(uploadDir, storedName)

        out, err := os.Create(dst)
        if err != nil {
            shared.WriteServerError(w, r, fmt.Errorf("create file: %w", err))
            return
        }
        defer out.Close()

        size, err := io.Copy(out, file)
        if err != nil {
            os.Remove(dst)
            shared.WriteServerError(w, r, fmt.Errorf("write file: %w", err))
            return
        }

        ct := header.Header.Get("Content-Type")
        if ct == "" {
            ct = "application/octet-stream"
        }
        // Strip parameters (e.g. "image/jpeg; name=...").
        if idx := strings.Index(ct, ";"); idx != -1 {
            ct = strings.TrimSpace(ct[:idx])
        }

        externalURL := "/api/v1/attachments/" + storedName
        now := shared.Now()
        att := &TaskAttachment{
            ID: shared.NewUUID(), TaskID: taskID,
            Filename: header.Filename, ContentType: ct,
            SizeBytes: size, ExternalURL: externalURL,
            CreatedAt: now,
        }
        if err := h.attachments.Create(att); err != nil {
            os.Remove(dst)
            shared.WriteServerError(w, r, err)
            return
        }

        actorID := shared.GetUserID(r)
        _ = h.activity.Write(t.ProjectID, taskID, actorID,
            "TASK_ATTACHMENT_ADDED", "attachment added: "+header.Filename)

        shared.WriteJSON(w, http.StatusCreated, att)
    }
}

func ServeAttachment(uploadDir string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        name := chi.URLParam(r, "filename")
        // Prevent directory traversal.
        if strings.Contains(name, "/") || strings.Contains(name, "..") {
            shared.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
                "invalid filename")
            return
        }
        path := filepath.Join(uploadDir, name)
        http.ServeFile(w, r, path)
    }
}
```

#### 2d — Wire routes in `handler.go` and `main.go`

In `wmHandler.RegisterRoutes` (or in `main.go` directly, since `UploadAttachment`
needs `uploadDir` which is runtime config):

```go
// In main.go, after wmHandler is created:
r.Group(func(r chi.Router) {
    r.Use(auth.JWTMiddleware(emailProvider))
    r.Use(shared.RequireJSON)
    // ... existing routes ...
    r.Post("/api/v1/tasks/{taskId}/attachments/upload",
        wmHandler.UploadAttachment(uploadDir))
})
// Serve files (no JWT required — URL is opaque enough):
r.Get("/api/v1/attachments/{filename}", workmanagement.ServeAttachment(uploadDir))
```

**Note**: `RequireJSON` middleware rejects non-JSON `Content-Type` requests
including `multipart/form-data`. The upload route must be registered **outside**
the `RequireJSON` group or the middleware must be made to allow multipart.
The simplest fix: register the upload route in a separate group without
`RequireJSON` but still with `JWTMiddleware`.

#### 2e — `TaskAttachmentRepo.Create` — verify it exists

Check `workmanagement/repo.go` for `func (r *TaskAttachmentRepo) Create`. If it
does not exist, add it:
```go
func (r *TaskAttachmentRepo) Create(a *TaskAttachment) error {
    _, err := r.db.Exec(
        `INSERT INTO task_attachments
         (id,task_id,filename,content_type,size_bytes,external_url,created_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7)`,
        a.ID, a.TaskID, a.Filename, a.ContentType,
        a.SizeBytes, a.ExternalURL, a.CreatedAt)
    return err
}
```

#### 2f — `.containerignore` / `.gitignore`

Ensure the upload directory is excluded from git and container builds:
```
# .gitignore (repo root)
octbase-api/uploads/

# octbase-api/.containerignore
uploads/
```

Also add to `podman-compose.yml` a volume mount so uploads survive container
restarts:
```yaml
# under the api service:
volumes:
  - ./uploads:/app/uploads
```

### Frontend changes

#### 2g — Replace the metadata-only form in `renderTaskAttachments`

Replace the text-field form with a file picker:
```js
<div class="attachment-upload-form">
  <input type="file" id="attach-file-input" class="attach-file-input"
    onchange="handleAttachmentFileSelected()">
  <label for="attach-file-input" class="btn btn-secondary btn-sm attach-file-label">
    Choose file
  </label>
  <span id="attach-file-name" class="text-muted text-sm">No file chosen</span>
  <button class="btn btn-primary btn-sm" id="attach-upload-btn"
    onclick="uploadAttachment('${task.id}')" disabled>Upload</button>
</div>
<div id="attach-progress" class="hidden text-muted text-sm">Uploading…</div>
```

#### 2h — `handleAttachmentFileSelected()`

```js
function handleAttachmentFileSelected() {
  const input = el('#attach-file-input');
  const nameEl = el('#attach-file-name');
  const btn = el('#attach-upload-btn');
  if (!input?.files?.length) return;
  const f = input.files[0];
  if (f.size > 10 * 1024 * 1024) {
    toast('File must be ≤ 10 MB', 'error');
    input.value = '';
    return;
  }
  if (nameEl) nameEl.textContent = f.name;
  if (btn) btn.disabled = false;
}
```

#### 2i — `uploadAttachment(taskId)`

```js
async function uploadAttachment(taskId) {
  const input = el('#attach-file-input');
  if (!input?.files?.length) return;
  const btn = el('#attach-upload-btn');
  const progress = el('#attach-progress');
  if (btn) btn.disabled = true;
  if (progress) progress.classList.remove('hidden');
  try {
    const formData = new FormData();
    formData.append('file', input.files[0]);
    const resp = await fetch(
      API_BASE + BASE_PATH + `/tasks/${taskId}/attachments/upload`,
      {
        method: 'POST',
        headers: { 'Authorization': 'Bearer ' + Auth.token },
        credentials: 'include',
        body: formData,
        // No Content-Type header — let the browser set multipart boundary.
      }
    );
    if (!resp.ok) {
      const body = await resp.json().catch(() => ({}));
      throw new Error(body.message || `Upload failed (${resp.status})`);
    }
    toast('File uploaded', 'success');
    S.taskPanelTab = 'attachments';
    await renderTaskPanel(taskId);
  } catch(e) {
    toast(e.message, 'error');
    if (btn) btn.disabled = false;
  } finally {
    if (progress) progress.classList.add('hidden');
  }
}
```

**Note**: the `api.http._fetch` wrapper always sets `Content-Type: application/json`
which breaks multipart upload. Use `fetch` directly for the upload call (as shown
above), not the `api` helper.

#### 2j — Display images inline

In `renderTaskAttachments`, detect image attachments and render them inline:
```js
const isImage = att.contentType.startsWith('image/');
const preview = isImage
  ? `<img src="${esc(att.externalUrl)}" class="attachment-preview" alt="${esc(att.filename)}">`
  : '';
```

```css
/* in app.css */
.attachment-preview { max-width: 100%; max-height: 200px; border-radius: var(--radius);
  display: block; margin-top: 6px; cursor: pointer; }
.attach-file-input { position: absolute; width: 1px; height: 1px; opacity: 0; }
.attach-file-label { cursor: pointer; display: inline-flex; }
.attachment-upload-form { display: flex; align-items: center; gap: 8px;
  flex-wrap: wrap; padding-top: 12px; }
```

#### 2k — Frontend API stub

Add to the `api` object in `app.js`:
```js
// attachments already has list/add/del; the upload uses fetch directly (see uploadAttachment)
```

No change to the `api` object needed — `uploadAttachment` calls `fetch` directly.

---

## Testing

### Backend unit / integration tests

Add to `handler_test.go`:

```go
func TestUploadAttachment_OK(t *testing.T) {
    // Use os.TempDir() for uploadDir in the test server variant.
    // This test verifies the route returns 201 and the attachment is listed.
}

func TestUploadAttachment_TooLarge(t *testing.T) {
    // Send a body > 10 MB and expect 413.
}

func TestUploadAttachment_RequiresMembership(t *testing.T) {
    // Use SecondUserID (not a member) and expect 403.
}
```

To support `uploadDir` in the test server, add a parameter to
`testutil.NewTestServer` or create a variant `testutil.NewTestServerWithUploads`.

### Manual browser test checklist

1. Open a task panel.
2. Edit the description with bold/italic/list formatting. Save. Reopen — verify
   formatting is preserved.
3. Upload a PNG image. Verify it appears inline in the Files tab.
4. Upload a PDF. Verify it shows as a download link.
5. Upload a file > 10 MB. Verify the client-side error toast fires before the
   upload starts.
6. Delete an attachment. Verify it disappears from the list.
7. Reload the page and reopen the task. Verify the rich-text description
   and attachments are still there.

---

## Implementation order

1. **Feature 2 backend** — Go migration is not needed (no schema change).
   `task_attachments` already has `external_url`. Add `attachment_handler.go`,
   wire routes, add tests. Commit.
2. **Feature 2 frontend** — replace the form in the attachments tab, add CSS.
   Sync `app.js` / `app.css` to `octbase-api/web/`. Commit.
3. **Feature 1** — add Quill CDN links to `index.html`, update
   `renderTaskDetails`, add `initDescriptionEditor`. Sync files. Commit.
4. **Manual test** — run the full browser checklist above.

---

## What NOT to do

- Do not add S3, GCS, or any cloud storage — local filesystem only for MVP.
- Do not add chunked / resumable upload — single-shot multipart only.
- Do not add a markdown-to-HTML pipeline for descriptions — Quill stores HTML
  directly.
- Do not change the `pages` feature (it has its own separate render-preview
  pipeline via the backend and uses a different editor pattern).
- Do not add image resizing, thumbnail generation, or virus scanning.
- Do not add authentication to the `GET /api/v1/attachments/{filename}` route
  for MVP — the UUID prefix makes filenames unguessable. Add auth post-MVP if
  needed.
