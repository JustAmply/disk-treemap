# Disk Treemap Branding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a generated project logo and favicon, serve them from the existing static web directory, and integrate the logo into the app header without regressing static asset delivery.

**Architecture:** Keep the branding change entirely inside the existing static web surface. Add one handler-level regression test that proves the root document references the new assets and that the favicon asset is directly servable, then add the generated image files plus minimal HTML and CSS updates for layout.

**Tech Stack:** Go `net/http` handler tests, static HTML/CSS, built-in `image_gen` tool

---

### Task 1: Lock Down Static Branding Behavior With a Failing Test

**Files:**
- Modify: `internal/api/handlers_test.go`
- Test: `internal/api/handlers_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestStaticIndexReferencesBrandingAssets(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	st := newTestStore(t, dataDir)
	svc := app.NewService(cfg, st)
	h := NewHandler(svc, cfg, filepath.Join("..", "..", "web"))
	mux := http.NewServeMux()
	h.Register(mux)

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	mux.ServeHTTP(indexRec, indexReq)

	if indexRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for index, got %d: %s", indexRec.Code, indexRec.Body.String())
	}

	body := indexRec.Body.String()
	for _, needle := range []string{
		`href="/assets/favicon.png"`,
		`src="/assets/disk-treemap-logo.png"`,
		`alt="Disk Treemap logo"`,
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected index to contain %q", needle)
		}
	}

	faviconReq := httptest.NewRequest(http.MethodGet, "/assets/favicon.png", nil)
	faviconRec := httptest.NewRecorder()
	mux.ServeHTTP(faviconRec, faviconReq)

	if faviconRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for favicon, got %d", faviconRec.Code)
	}
	if got := faviconRec.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/png") {
		t.Fatalf("expected png content type, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api -run TestStaticIndexReferencesBrandingAssets -count=1`

Expected: FAIL because the current `index.html` does not yet reference `/assets/favicon.png` or `/assets/disk-treemap-logo.png`, and the favicon asset file does not exist.

- [ ] **Step 3: Keep the failure focused**

If the test fails for import errors only, add the missing standard library import:

```go
import "strings"
```

Re-run the same command until the test fails on the missing branding behavior rather than a compile problem.

- [ ] **Step 4: Commit the red test**

```bash
git add internal/api/handlers_test.go
git commit -m "test: cover branding static assets"
```

### Task 2: Generate and Add the Branding Assets

**Files:**
- Create: `web/assets/disk-treemap-logo.png`
- Create: `web/assets/favicon.png`

- [ ] **Step 1: Generate the master logo asset**

Use the built-in `image_gen` tool with a prompt equivalent to:

```text
Use case: logo-brand
Asset type: self-hosted web app logo
Primary request: create a square icon-first logo for a project named Disk Treemap
Subject: a rounded-square app mark combining a simplified folder silhouette with large treemap-style rectangles
Style/medium: clean vector-friendly raster logo, technical and precise, transparent background
Composition/framing: centered symbol, balanced negative space, readable at small sizes
Lighting/mood: flat graphic finish, no gloss
Color palette: deep slate base with cool cyan, teal, and one brighter blue highlight
Constraints: no text, no mascot, no tiny details, no watermark
Avoid: gradients required for recognition, thin strokes, photorealism
```

- [ ] **Step 2: Generate the favicon variant**

Use the built-in `image_gen` tool with a prompt equivalent to:

```text
Use case: logo-brand
Asset type: browser favicon
Primary request: create a simplified favicon version of the same Disk Treemap icon
Subject: the same rounded-square folder-plus-treemap symbol with fewer, larger interior cells
Style/medium: crisp flat graphic, transparent background
Composition/framing: centered icon with high contrast and strong outer silhouette
Color palette: deep slate with bright cyan and teal accents
Constraints: no text, no fine detail, must stay legible at 16x16 and 32x32, no watermark
Avoid: extra shapes, shadows, gradients, micro-lines
```

- [ ] **Step 3: Move the selected outputs into the workspace**

Expected final paths:

```text
web/assets/disk-treemap-logo.png
web/assets/favicon.png
```

- [ ] **Step 4: Verify the files exist before HTML changes**

Run:

```bash
Get-ChildItem web\assets
```

Expected: both PNG files are listed with the final names above.

- [ ] **Step 5: Commit the assets**

```bash
git add web/assets/disk-treemap-logo.png web/assets/favicon.png
git commit -m "feat: add disk treemap branding assets"
```

### Task 3: Integrate the Logo and Favicon Into the UI

**Files:**
- Modify: `web/index.html`
- Modify: `web/app.css`

- [ ] **Step 1: Add favicon links and header logo markup**

Update `web/index.html` so the document head includes:

```html
<link rel="icon" type="image/png" href="/assets/favicon.png" />
```

And update the header heading block to include:

```html
<div class="app-brand">
  <img class="app-logo" src="/assets/disk-treemap-logo.png" alt="Disk Treemap logo" />
  <div class="app-heading">
    <h1>Disk Treemap</h1>
    <div class="header-meta">
      <span class="root-tag">Root</span>
      <p id="rootPath" class="root-path">Loading root...</p>
    </div>
  </div>
</div>
```

- [ ] **Step 2: Add the minimal CSS for the new brand row**

Update `web/app.css` with layout rules equivalent to:

```css
.app-brand {
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 0.95rem;
}

.app-logo {
  width: clamp(3rem, 4.6vw, 4.25rem);
  height: clamp(3rem, 4.6vw, 4.25rem);
  flex-shrink: 0;
  display: block;
  filter: drop-shadow(0 14px 28px rgba(18, 32, 50, 0.14));
}
```

Keep the existing `.app-heading` structure and adapt responsive rules so the new image stacks cleanly on narrow screens.

- [ ] **Step 3: Preserve mobile behavior**

Update the small-screen media rules so the brand block stays aligned without forcing the title or logo off-screen. The intended end state is:

```css
@media (max-width: 640px) {
  .app-brand {
    align-items: flex-start;
  }
}
```

- [ ] **Step 4: Commit the UI integration**

```bash
git add web/index.html web/app.css
git commit -m "feat: wire branding into web header"
```

### Task 4: Prove the End-to-End Change Is Green

**Files:**
- Test: `internal/api/handlers_test.go`

- [ ] **Step 1: Run the focused handler test**

Run: `go test ./internal/api -run TestStaticIndexReferencesBrandingAssets -count=1`

Expected: PASS

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: Inspect the branding diff**

Run:

```bash
git diff --stat HEAD~3..HEAD
```

Expected: new asset files plus small updates in `web/index.html`, `web/app.css`, and `internal/api/handlers_test.go`.

- [ ] **Step 4: Final commit if verification required follow-up edits**

```bash
git add internal/api/handlers_test.go web/index.html web/app.css web/assets/disk-treemap-logo.png web/assets/favicon.png
git commit -m "test: verify branding asset integration"
```
