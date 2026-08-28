# 技术设计

## 边界

本子任务只优化 115 同 Connection 的 cloud transfer 与 transfer staging cleanup。通用 `cloud.Driver` 和单项 `MutationDriver` 保持兼容；批量能力通过可选接口扩展，其他驱动继续走现有路径。

## 数据流

```text
private source manifest
  -> prove Storage root -> package root once
  -> list unique source parents / bounded package tree
  -> exact ID + parent + size + optional SHA1 reconciliation
  -> build target directory DAG and shared target listings
  -> group by operation + target parent + bounded chunk
  -> persist private batch intent
  -> provider batch call outside DB transaction
  -> reconcile each item from one target-parent listing
  -> short per-batch checkpoint transaction
  -> final dirty_generation handoff
```

Cleanup reuses the same proven package-root/source-parent snapshot, validates the complete safe difference first, then performs bounded `RecycleMany` chunks. Missing is idempotent; changed/ambiguous identity fails closed.

## Contracts

- Add optional `cloud.BatchMutationDriver` with `MoveMany`, `CopyMany`, `RecycleMany`; `RenameMany` is enabled only if the selected 115 SDK endpoint can be validated with bounded request/response tests.
- Pan115 adapter maps batch calls to the dependency's variadic primitives. Chunk size is a named bounded constant covered by request-size tests, not a user-controlled value.
- Private checkpoint records batch intent and per-item result. Job payload, DTO, log and audit contain no provider IDs or paths.
- Source proof is authoritative only when complete. A partial bulk tree cannot authorize mutations; fallback lists each unique parent within the immutable package root.
- Existing conflict policies, copy temporary directory, stable-ID restart behavior, protected leftovers and same-Connection rules remain unchanged.
- Existing error-driven shared risk controller remains the only `risk_backoff` source. Endpoint limiters apply once per batch request.

## Compatibility and rollout

- No existing task schema needs destructive migration; extend private `cloud_state_json` with a versioned optional batch-intent section and retain the legacy reader.
- In-flight legacy tasks without batch state enter the new preflight and may use batch operations only after the complete current manifest is revalidated.
- If batch capability is absent, use the existing singleton path; correctness is identical but performance remains provider-dependent.

## Rollback

Batch operations are behind capability detection. A rollback can disable the Pan115 batch capability and retain existing checkpoint data; the singleton reconciler must ignore unknown optional fields safely.

## Evidence

- `research/115-transfer-performance.md`
- `.trellis/spec/backend/transfer-organization.md`
- `.trellis/spec/backend/media-library-foundation.md`
- `.trellis/spec/backend/security-guidelines.md`
