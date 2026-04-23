# Disk Treemap Branding Design

Date: 2026-04-23
Topic: Project logo and favicon

## Goal

Add a project logo and favicon that fit the existing Disk Treemap web UI and clearly communicate storage plus treemap exploration. The assets should be legible in the application header, browser tab, and repository documentation without introducing a heavier brand system than the product needs.

## Selected Direction

Use an icon-first mark built around a rounded square container with a simplified folder silhouette and treemap-like internal rectangles. The tone should feel technical and precise rather than playful. The palette should stay in the current product family: dark slate as the base with cool blue and teal accent shapes.

This direction is preferred because it scales well across two very different contexts:

- a larger header logo where the symbol sits beside the existing `Disk Treemap` text
- a very small favicon where the same symbol must still read as a distinct app mark

## Alternatives Considered

### Circular segmented disk icon

This option would map more literally to the word `Disk`, but it depends on narrow segments that lose clarity at favicon size.

### Stacked utility badge

This option would be clean and compact, but it does not communicate the treemap concept as directly unless the composition becomes too busy.

## Asset Plan

Create two raster assets for the current project:

1. `web/assets/disk-treemap-logo.png`
2. `web/assets/favicon.png`

The logo asset should be a transparent square master image at high enough resolution for header use and documentation reuse. The favicon asset should be a simplified version of the same mark with stronger contrast and fewer interior subdivisions so it remains legible at small browser-tab sizes.

## Visual Specification

### Shape language

- Rounded square outer silhouette
- Strong interior negative space
- Simplified folder cue rather than a literal file-browser illustration
- Treemap rectangles limited to a small number of large cells

### Color

- Base: deep slate or charcoal-blue
- Accents: cool cyan and teal with one brighter blue highlight
- Background: transparent

### Style constraints

- No text inside the generated image
- No gradients that are required for recognition
- No thin strokes or micro-detail that collapse at `16x16`
- No mascots, anthropomorphic elements, or marketing-style gloss

## UI Integration

Update `web/index.html` so the application header includes the new logo image next to the existing `Disk Treemap` title text. Keep the title text in HTML rather than baking it into the asset so typography remains crisp and accessible.

Add favicon links in the document `<head>` so the browser tab uses the generated mark.

Add any needed CSS in `web/app.css` to support:

- horizontal alignment between logo and title block
- a constrained logo size that fits the current header height
- responsive behavior that preserves layout on smaller screens

## Implementation Notes

- Store assets in `web/assets/`
- Prefer version-stable filenames from the start so HTML references do not need churn
- Keep the header structure close to the current layout and avoid unrelated refactors
- Do not change application copy or navigation behavior as part of this work

## Validation

After implementation, verify:

1. the header logo renders correctly on desktop and mobile widths
2. the browser tab loads the new favicon
3. the mark remains distinct against the existing page background and header styling
4. the icon still reads clearly when visually reduced to favicon scale

## Out of Scope

- creating a broader brand guideline system
- replacing the README overview image
- introducing SVG redraws or a custom typography system
- changing product naming
