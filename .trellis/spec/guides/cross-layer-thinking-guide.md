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
- [ ] For provider redirects, traced `provider API request → SDK response → adapter TemporaryURL → signed resolver → client 302` and distinguished URL-acquisition headers from final playback requirements. Copying an SDK header map across that boundary is forbidden; regression fixtures must use the complete real header shape while asserting that credentials never reach the redirect, cache, database, or logs.
- [ ] For browser media redirects, checked the final CDN response under actual HTMLMediaElement CORS semantics. A successful Server 302 is insufficient when the player sets `crossOrigin=anonymous`; either the CDN must authorize the document origin or a fixed, reviewed player compatibility patch must suppress CORS mode. Adding CORS headers to the intermediate 302 does not authorize the final response.
- [ ] For a browser compatibility patch, verified delivery through the real document navigation path, not only a direct unit request for the patched module. Account for Service Worker/Cache Storage reuse, assert that the HTML shell loads a fixed same-origin fallback before the application boot script, and expose a safe module log/response marker so a cache miss can be distinguished from a pattern miss.
- [ ] For retries, verified that recomputation uses immutable source facts rather than an old UI/cache projection; stale plans, progress and provider checkpoints are cleared or versioned before revalidation.
- [ ] When a task's logical classification may change after bytes were placed, traced physical source identity separately from logical metadata (`staging_category` versus `scrape_category`); transfer, cleanup and recovery must use the immutable physical snapshot, while naming and destination routing use the verified logical result.
- [ ] For completed-provider recovery, verified a bounded immutable manifest is persisted before recognition failure and that a retry cannot call submit/pause/resume/category APIs or depend on the provider still listing the completed task.
- [ ] For production parsing bugs, copied the complete real input into a regression fixture, preserving folder prefixes, dots, spaces, brackets and hyphens instead of testing only a cleaned sample.
- [ ] For PT/anime release parsing, covered the same untouched title at the pure parser and the shared service entry so built-in word packs, domain parsing, query budgeting and metadata lookup cannot drift. Include multilingual `/` aliases, bracket order, season/episode ranges and technical tokens from real search results; never patch one work name.
- [ ] Preserved every bounded provider-neutral recognition fact across the adapter/claim/service boundary: primary title, auxiliary subtitle/description, requested media type and explicit verified identity have different trust and weight. Do not silently drop useful context or concatenate it into one trusted title.
- [ ] Every new parser cleanup rule has at least one real positive fixture and one legal-title counterexample. A larger regex without a counterexample is not a completed recognition fix.
- [ ] When `mediarecognition.EngineVersion` changes, searched every consumer/cache key and kept the WebUI recognition-session version synchronized. A cross-layer test must fail if the Go and TypeScript constants drift.
- [ ] Kept the complete built-in `tv-v1` and `anime-v1` packs enabled in an integration regression; parser-only success does not prove the production Profile pipeline still uses them.
- [ ] When several providers produce the same domain object, kept parsing/classification/naming before the provider adapter boundary so one fix covers every provider.
- [ ] When one downloader protocol cannot consume the discovery artifact, converted it only at the downloader capability boundary (for example raw `.torrent` `info` bytes to BTIH magnet), preserving identity exactly and keeping passkeys in the encrypted source envelope.
- [ ] When a query has an explicit provider/Site scope, traced that scope through the initial request, SSE/JSON fallback, multilingual variants, retry, pagination and browser restoration; no missing scope may silently widen to all providers.
- [ ] When task-level metadata and per-item evidence coexist, wrote and tested one precedence resolver used by validation, planning, persisted summaries, catalog output and corrective retries; a package-level fallback must not overwrite conflicting per-file facts.
- [ ] For destructive previews, separated local-record validation from provider reconciliation, bounded every external call end to end, batched by provider parent/root where possible, and treated missing/detached facts as convergent states without expanding the deletion boundary.

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
