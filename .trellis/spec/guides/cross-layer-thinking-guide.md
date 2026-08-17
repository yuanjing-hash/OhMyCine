# Cross-Layer Thinking Guide

> **Purpose**: Think through data flow across layers before implementing.

---

## The Problem

**Most bugs happen at layer boundaries**, not within layers.

Common cross-layer bugs:
- API returns format A, frontend expects format B
- Database stores X, service transforms to Y, but loses data
- Multiple layers implement the same logic differently

---

## Before Implementing Cross-Layer Features

### Step 1: Map the Data Flow

Draw out how data moves:

```
Source → Transform → Store → Retrieve → Transform → Display
```

For each arrow, ask:
- What format is the data in?
- What could go wrong?
- Who is responsible for validation?

### Step 2: Identify Boundaries

| Boundary | Common Issues |
|----------|---------------|
| API ↔ Service | Type mismatches, missing fields |
| Service ↔ Database | Format conversions, null handling |
| Backend ↔ Frontend | Serialization, date formats |
| Component ↔ Component | Props shape changes |

### Step 3: Define Contracts

For each boundary:
- What is the exact input format?
- What is the exact output format?
- What errors can occur?

---

## Common Cross-Layer Mistakes

### Mistake 1: Implicit Format Assumptions

**Bad**: Assuming date format without checking

**Good**: Explicit format conversion at boundaries

### Mistake 2: Scattered Validation

**Bad**: Validating the same thing in multiple layers

**Good**: Validate once at the entry point

### Mistake 3: Leaky Abstractions

**Bad**: Component knows about database schema

**Good**: Each layer only knows its neighbors

---

## Checklist for Cross-Layer Features

Before implementation:
- [ ] Mapped the complete data flow
- [ ] Identified all layer boundaries
- [ ] Defined format at each boundary
- [ ] Decided where validation happens

After implementation:
- [ ] Tested with edge cases (null, empty, invalid)
- [ ] Verified error handling at each boundary
- [ ] Checked data survives round-trip
- [ ] For protocol bridges, executed one real end-to-end request through every layer instead of relying only on static assertions. Verify framework-specific route syntax, redirect behavior, security-header stripping, status codes, and streaming headers such as Range/Content-Range.

### Native Playback Bridge Example

Android remote playback crosses `DataSource → Vue invoke → Rust command → loopback HTTP router → upstream redirect/CDN → libmpv`. A successful compile proves only the types. The regression test must start local upstream endpoints and send an actual loopback request so an incorrect router pattern, an HTTP-to-HTTPS redirect bypass, or a lost Range header fails before an APK reaches a device.

### Native Window Event Timing Example

A frameless desktop player crosses `DOM pointer event → Tauri window API → native move loop → Tauri WindowEvent → Win32 video underlay`. Do not assume events that all sound like “geometry changed” share one timing contract:

- A native title-bar drag must start from the original primary-button press. Waiting for a movement threshold or an asynchronous maximize query can lose the OS gesture and make the first drag fail.
- A pure window move keeps the WebView surface rectangle unchanged inside the client area. Reposition a separate native underlay immediately from its cached confirmed bounds plus the owner's current screen origin; do not wait for a `ResizeObserver` that may never fire.
- Resize and DPI changes can change the surface size. Those paths must wait for the WebView's final layout bounds rather than deriving a replacement size from the native client rect, or the native video can visually lead the transparent UI.
- When the native event callback uses a non-blocking lock, check whether dropped intermediate events still converge to the final position. Coalesce or provide a final reconciliation path instead of assuming every event is delivered.

Regression tests should assert the distinct move/resize branches, the absence of delayed drag prerequisites, and the absence of a native-size fallback. Windows runtime testing must still cover first-drag success, Snap maximize, cross-monitor DPI changes, and continuous playback-window movement.

---

## When to Create Flow Documentation

Create detailed flow docs when:
- Feature spans 3+ layers
- Multiple teams are involved
- Data format is complex
- Feature has caused bugs before
