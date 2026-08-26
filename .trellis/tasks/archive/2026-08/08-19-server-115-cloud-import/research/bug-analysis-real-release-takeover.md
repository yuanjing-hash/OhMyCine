## Bug Analysis: real release names failed recognition and retries displayed stale advertisement plans

### 1. Root Cause Category

- **Category**: D/E — test coverage gap plus implicit token-boundary assumption.
- **Specific cause**: suffix cleanup split only on whitespace, so the production tail `0-SONYHD` remained one unrecognized token. Earlier tests used a simplified space-separated title. Separately, transfer re-verification replaced the private manifest only on success and never invalidated the previous public plan/checkpoint on failure.

### 2. Why Fixes Failed

1. Filtering small videos fixed provider mutation scope but did not fix the TMDB query anchor.
2. A simplified `Seven Samurai CC MA 2 0 SONYHD` unit test passed while the real dotted filename still produced `Seven Samurai CC MA 2 0-SONYHD`.
3. Re-verification failed closed for new writes, but the UI read a stale persisted plan and therefore appeared to keep processing old advertisement items.

### 3. Prevention Mechanisms

| Priority | Mechanism | Specific Action | Status |
|---|---|---|---|
| P0 | Architecture | Keep recognition/classification/naming provider-neutral; adapters only execute plans | DONE |
| P0 | Test coverage | Assert exact TMDB query/title/year from the complete real folder and filename | DONE |
| P0 | Runtime state | Clear stale plan/progress/cloud projection before legacy re-verification | DONE |
| P1 | Documentation | Record Profile snapshot and retry-projection contracts in backend specs | DONE |

### 4. Systematic Expansion

- **Similar issues**: qBittorrent, Transmission, 123/OpenList and future offline providers would drift if adapters owned parsing.
- **Design improvement**: Profile now owns ordered recognition preprocessing and naming, while DownloadTask snapshots it and TransferService remains the sole planning boundary.
- **Process improvement**: production bug fixtures must be copied in full, preserving dots, brackets, hyphens, folder prefixes and year placement.

### 5. Knowledge Capture

- [x] Updated media classification, media library and transfer specs.
- [x] Added retry/real-input checks to the cross-layer guide.
- [x] Added focused and integration regressions.
