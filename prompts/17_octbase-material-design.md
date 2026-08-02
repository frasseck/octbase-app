# Octbase — Material Design 3 Frontend Redesign

**Purpose:** Replace the custom Jira-inspired blue theme with a complete Material Design 3 (M3) implementation using a green primary palette. Update the logo and favicon to match.

**Scope:** `octbase-frontend/` only. No API, database, or backend changes.

**Reference:** https://m3.material.io/

---

## 0. Prerequisites

- The frontend is a single static bundle (`app.js`, `app.css`, `index.html`) built into an nginx container via `octbase-frontend/Containerfile`.
- All HTML is generated dynamically by `app.js`. CSS class names in `app.css` must match exactly what `app.js` emits — do not rename or remove any class.
- After every change, rebuild and redeploy the frontend container:

```bash
podman-compose build octbase-frontend
podman stop octbase_octbase-frontend_1 && podman rm octbase_octbase-frontend_1
podman-compose up -d octbase-frontend
```

---

## 1. Google Fonts

Add Roboto (weights 300, 400, 500, 700) and Roboto Mono (400) to `index.html` before `app.css`:

```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Roboto:wght@300;400;500;700&family=Roboto+Mono:wght@400&display=swap">
```

Set `font-family: 'Roboto', -apple-system, BlinkMacSystemFont, sans-serif` on `body`. Use `'Roboto Mono', monospace` for `code`, `pre`, `kbd`, branch names, and the page editor textarea.

---

## 2. M3 Color Token System

Replace all hardcoded hex values in `app.css` with CSS custom properties. Define the full token set in `:root`:

### 2.1 Green primary palette

```css
:root {
  /* Primary — deep forest green */
  --md-primary:                 #006C4F;
  --md-on-primary:              #FFFFFF;
  --md-primary-container:       #B0EDD0;
  --md-on-primary-container:    #002115;

  /* Secondary — sage green */
  --md-secondary:               #4A6455;
  --md-on-secondary:            #FFFFFF;
  --md-secondary-container:     #CCE8D6;
  --md-on-secondary-container:  #072015;

  /* Tertiary — olive (no blue) */
  --md-tertiary:                #5E6440;
  --md-on-tertiary:             #FFFFFF;
  --md-tertiary-container:      #E3E9BE;
  --md-on-tertiary-container:   #1B1E00;

  /* Error */
  --md-error:                   #BA1A1A;
  --md-on-error:                #FFFFFF;
  --md-error-container:         #FFDAD6;
  --md-on-error-container:      #410002;

  /* Background / Surface — warm green-white */
  --md-background:              #F6FAF6;
  --md-on-background:           #191D19;
  --md-surface:                 #F6FAF6;
  --md-on-surface:              #191D19;
  --md-surface-variant:         #D6E4D9;
  --md-on-surface-variant:      #3E4940;

  /* Surface containers — consistent green-tinted progression */
  --md-surface-cl:              #FFFFFF;      /* lowest  */
  --md-surface-c-low:           #F0F5F0;
  --md-surface-c:               #EAF0EA;
  --md-surface-c-high:          #E4EBE4;
  --md-surface-c-highest:       #DFE5DF;

  /* Outline */
  --md-outline:                 #6D7870;
  --md-outline-variant:         #BBCABD;

  /* Inverse (snackbar / bulk bar) */
  --md-inverse-surface:         #2D312D;
  --md-inverse-on-surface:      #ECF2ED;
  --md-inverse-primary:         #7ADCAD;
}
```

**Consistency rule:** all surface containers must share the same hue family (green-white). The tertiary role must not introduce blue; use olive or warm earth tones instead.

### 2.2 Elevation tokens

M3 uses a two-layer shadow formula instead of single-color shadows:

```css
--md-e1: 0 1px 2px rgba(0,0,0,.3),  0 1px 3px 1px rgba(0,0,0,.15);
--md-e2: 0 1px 2px rgba(0,0,0,.3),  0 2px 6px 2px rgba(0,0,0,.15);
--md-e3: 0 1px 3px rgba(0,0,0,.3),  0 4px 8px 3px rgba(0,0,0,.15);
--md-e4: 0 2px 3px rgba(0,0,0,.3),  0 6px 10px 4px rgba(0,0,0,.15);
```

### 2.3 Shape tokens

```css
--md-xs:   4px;
--md-sm:   8px;
--md-md:   12px;
--md-lg:   16px;
--md-xl:   28px;
--md-full: 9999px;
```

### 2.4 Legacy aliases

`app.js` generates HTML that references these legacy variable names. Keep them as aliases so no JS changes are needed:

```css
--primary:       var(--md-primary);
--primary-light: var(--md-primary-container);
--sidebar-bg:    var(--md-surface-c-low);
--bg:            var(--md-background);
--surface:       var(--md-surface-cl);
--border:        var(--md-outline-variant);
--text:          var(--md-on-background);
--muted:         var(--md-on-surface-variant);
--radius:        var(--md-xs);
--radius-lg:     var(--md-md);
--shadow:        var(--md-e1);
--shadow-md:     var(--md-e2);
```

---

## 3. Layout Shell

| Element | M3 treatment |
|---------|-------------|
| `#sidebar` | `background: var(--md-surface-c-low)` + right border in `--md-outline-variant` |
| `#topbar` | `background: var(--md-surface-c-low)`, `height: 64px`, bottom border |
| `#content` | `background: var(--md-background)` via body |

---

## 4. Navigation Drawer (Sidebar)

The sidebar becomes a light M3 Navigation Drawer. Key rules:

**Do not use `::before` pseudo-elements with `z-index` for the active pill.** Bare text nodes inside flex items are painted at the parent level — a `z-index: 0` pseudo-element will cover them and make the label text invisible when the item is active.

The correct approach: apply `background`, `border-radius`, and `margin` directly on `.sidebar-item`:

```css
.sidebar-item {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 56px;
  padding: 0 16px;
  margin: 2px 8px;
  border-radius: var(--md-full);          /* pill shape */
  color: var(--md-on-surface-variant);
  cursor: pointer;
  font-size: 14px;
  font-weight: 500;
  transition: background .15s;
}
.sidebar-item:hover  { background: rgba(0,108,79,.08); }
.sidebar-item.active { background: var(--md-secondary-container);
                       color: var(--md-on-secondary-container); }
```

The `margin: 2px 8px` provides the M3 spec's 12 dp inset on each side of the pill. No z-index manipulation is needed.

User avatar in the sidebar footer uses `--md-primary-container` / `--md-on-primary-container` instead of the old blue.

---

## 5. Component Styles

### 5.1 Buttons

All buttons: `border-radius: var(--md-full)` (pill), `font-weight: 500`, `letter-spacing: .1px`.

| Class | Style |
|-------|-------|
| `.btn-primary` | `background: --md-primary`, white text |
| `.btn-secondary` | `background: --md-secondary-container`, `--md-on-secondary-container` text |
| `.btn-ghost` | transparent bg, `--md-primary` text |
| `.btn-danger` | `background: --md-error`, white text |

### 5.2 Cards

`.card`, `.board-card`: `border-radius: var(--md-md)` (12px), `box-shadow: var(--md-e1)`, `background: var(--md-surface-cl)`. Hover lifts to `--md-e2`.

### 5.3 Modals / Dialogs

`.modal`: `border-radius: var(--md-xl)` (28px), `background: var(--md-surface-c-highest)`, scrim `rgba(0,0,0,.32)`.

### 5.4 Form fields (Outlined text field)

`.form-input`, `.form-select`, `.form-textarea`: `border: 1px solid --md-outline`, `border-radius: var(--md-xs)`. On focus: `border-width: 2px`, `border-color: --md-primary`, compensate padding by 1px to avoid layout shift.

### 5.5 Badges / Chips

`.badge`: `border-radius: var(--md-full)`, `padding: 4px 10px`, `font-weight: 500`.

| Badge | Background | Text |
|-------|-----------|------|
| `badge-in-progress` | `--md-primary-container` | `--md-on-primary-container` |
| `badge-done` / `badge-active` | `--md-secondary-container` | `--md-on-secondary-container` |
| `badge-in-review` | `#EDE7FF` | `#4527A0` |
| `badge-planned` | `--md-surface-c-high` | `--md-on-surface-variant` |
| `badge-archived` / `badge-closed` | `--md-surface-c-highest` | `--md-on-surface-variant` |

### 5.6 Progress bars

`.milestone-progress`, `.release-progress`: `height: 4px`, `appearance: none`, `border-radius: var(--md-full)`. Style `::-webkit-progress-value` and `::-moz-progress-bar` with `--md-primary`.

### 5.7 Toast (Snackbar)

`#toast-container`: `left: 50%; transform: translateX(-50%)` (centered, not bottom-right).  
`.toast-success` / `.toast-info`: `background: --md-inverse-surface`, `color: --md-inverse-on-surface`.

### 5.8 Command palette

`.palette-box`: `border-radius: var(--md-xl)`, `background: --md-surface-c-high`.  
Active item: `background: --md-primary-container`, `color: --md-on-primary-container`.

### 5.9 Board columns

`.board-col`, `.board-column`: `background: --md-surface-c`, `border-radius: var(--md-md)`. Drag-over state: `background: --md-primary-container`.

---

## 6. Logo

Replace `logo.png` with a vector SVG wordmark. Create `octbase-frontend/logo.svg`:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 260 64">
  <text x="2" y="50"
        font-family="-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Arial, sans-serif"
        font-size="52">
    <tspan font-weight="600" fill="#191D19">task</tspan>
    <tspan font-weight="400" fill="#006C4F">base</tspan>
  </text>
</svg>
```

- "task" — semi-bold, near-black
- "base" — regular weight, primary green
- No icon, no square, no checkmark — wordmark only

Update CSS: `.logo-img { max-width: 160px; }` (up from 120px to accommodate the wider text-only mark).

Update `app.js` line that renders the sidebar logo:
```js
// before
<img class="logo-img" src="logo.png" alt="Octbase">
// after
<img class="logo-img" src="logo.svg" alt="Octbase">
```

---

## 7. Favicon

### 7.1 SVG favicon

Create `octbase-frontend/favicon.svg` — a green upward-pointing triangle on a transparent background:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <polygon points="16,3 31,29 1,29" fill="#006C4F"/>
</svg>
```

### 7.2 ICO favicon

Generate `favicon.ico` with 7 sizes (16, 24, 32, 48, 64, 128, 256 px) using Python / Pillow. Use 4× supersampling + LANCZOS downscale for clean edges at small sizes:

```python
from PIL import Image, ImageDraw

def make_icon(size):
    scale = 4
    s = size * scale
    img = Image.new('RGBA', (s, s), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    top   = (s / 2,     s * 0.09)
    left  = (s * 0.03,  s * 0.91)
    right = (s * 0.97,  s * 0.91)
    draw.polygon([top, right, left], fill=(0, 108, 79, 255))
    return img.resize((size, size), Image.LANCZOS)

sizes = [16, 24, 32, 48, 64, 128, 256]
imgs  = [make_icon(s) for s in sizes]
imgs[0].save(
    'octbase-frontend/favicon.ico',
    format='ICO',
    sizes=[(s, s) for s in sizes],
    append_images=imgs[1:],
)
```

### 7.3 HTML wiring

```html
<link rel="icon" href="favicon.ico">
<link rel="icon" type="image/svg+xml" href="favicon.svg">
```

Modern browsers pick up the SVG (crisp at any DPI); the ICO is the fallback.

---

## 8. Containerfile

The `COPY` line in `octbase-frontend/Containerfile` is an explicit file list. Add the new assets:

```dockerfile
# before
COPY app.js app.css index.html favicon.ico logo.png /usr/share/nginx/html/

# after
COPY app.js app.css index.html favicon.ico favicon.svg logo.png logo.svg /usr/share/nginx/html/
```

---

## 9. Verification Checklist

- [ ] Sidebar background is light (green-tinted white), not dark
- [ ] Clicking a nav item shows the active pill highlight **and the label text is visible**
- [ ] All buttons are pill-shaped (full border-radius)
- [ ] Cards have 12 dp corner radius and M3 two-layer shadow
- [ ] Modals/dialogs have 28 dp corner radius
- [ ] No blue colour appears anywhere in the UI
- [ ] Primary container (`#B0EDD0`) is a soft green, not cyan or teal
- [ ] Tertiary container (`#E3E9BE`) is olive, not blue
- [ ] All surface containers are in the same warm green-white family
- [ ] Font renders as Roboto (check DevTools → Network → Fonts)
- [ ] Logo shows "**task**base" wordmark — no icon, no square, no checkmark
- [ ] Favicon is a green triangle on transparent background
- [ ] Both `favicon.svg` and `favicon.ico` are served (`curl -I http://localhost:8080/favicon.svg` returns 200)
- [ ] Both `logo.svg` and `logo.png` are served (old PNG kept for compatibility)
- [ ] Hard reload clears any cached blue favicon from the browser tab
