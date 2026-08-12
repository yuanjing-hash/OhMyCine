# Implementation Plan

1. Load backend API/database/security/web-admin specs and current migration/RBAC patterns.
2. Add Storage permission catalog entries, generated frontend constants and role seeds.
3. Add versioned migration and Storage model with uniqueness/reference constraints.
4. Implement local path canonicalization/probe abstraction and Windows-focused unit tests.
5. Implement Storage service, audit actions, thin handlers and protected routes.
6. Implement Storage management UI, API types/client calls and permission-aware controls.
7. Update Server architecture/Web UI/roadmap docs for Storage vs Destination and local-first order.
8. Run `server/test.ps1`, migration upgrade tests, API RBAC tests and `git diff --check`.
9. Perform read-only live acceptance against the user-provided runtime media root; do not persist its absolute path in repository files.
