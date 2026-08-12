# Research: Safe Git migration from `main` to `origin/develop`

- **Query**: Move only the current uncommitted tracked/untracked changes from `main` to the latest `origin/develop`, delete all of `.claude` and `.trellis/tasks/00-join-yuanjing`, preserve every other change, and keep rollback possible.
- **Scope**: internal
- **Date**: 2026-08-11

## Findings

### Files Found

| File Path | Description |
|---|---|
| `.claude/` | Mixed tracked/untracked target to delete: 34 modified tracked files and 16 untracked files. `origin/develop` currently contains 39 tracked files here. |
| `.trellis/tasks/00-join-yuanjing/` | Target to delete; currently two untracked files and absent from `origin/develop`. |
| `.trellis/tasks/08-11-migrate-uncommitted-to-develop/` | Active task; its untracked task files and this research file are part of the changes that must otherwise survive. |

### Repository State Observed

- Branch is `main` at `ba22a35788b01c379d116dc27a1f48620a35c7f9`, exactly equal to the inspected `origin/main`.
- The inspected local remote-tracking ref `origin/develop` is `a01ae819f6fa2b9e540a2adb84a49e9089fa3a89`, 14 commits ahead of and a direct descendant of `main` (`0 14` from `git rev-list --left-right --count HEAD...origin/develop`). Fetch again immediately before migration; the inspected ref is not proof that the server has not advanced.
- Working tree has 147 visible changes: 76 modified tracked files, 71 untracked files, and no staged changes. No ignored-file collision was found with the 98 paths added between `main` and the inspected `origin/develop`.
- Five locally modified tracked paths were also changed on `origin/develop`: `.github/workflows/player.yml`, `.trellis/spec/backend/directory-structure.md`, `.trellis/spec/frontend/quality-guidelines.md`, `AGENTS.md`, and `player/package.json`. `git stash apply` can three-way merge these, but conflicts must be resolved by preserving both the new `develop` baseline and the pre-existing local edit.
- There is no committed path delta under `.claude` between the inspected `main` and `origin/develop`, which reduces deletion/apply ambiguity for that directory.

### Recommended Procedure (PowerShell)

Use a quiet maintenance window: no agent, editor formatter, hook, or build process should write the shared worktree between the initial stash and final verification.

1. Fetch and enforce the assumptions. Abort instead of improvising if any check fails.

```powershell
Set-Location 'D:\Development\Code\OhMyCine'
git fetch --prune origin
if ((git branch --show-current) -ne 'main') { throw 'Expected main' }
if ((git rev-parse HEAD) -ne (git rev-parse origin/main)) { throw 'main no longer equals origin/main' }
git merge-base --is-ancestor HEAD origin/develop
if ($LASTEXITCODE -ne 0) { throw 'origin/develop is no longer a descendant of main; reassess migration' }
git diff --name-only --diff-filter=U
if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect conflicts' }
git status --short --branch --untracked-files=all
```

2. Stash tracked plus ordinary untracked files, **apply rather than pop later**, and retain the stash as the rollback source. `-u` deliberately does not include ignored build/runtime files; they remain in place. The inspection found no ignored collision with files added on `develop`.

```powershell
git stash push --include-untracked --message 'safety: pre-develop migration 2026-08-11'
if ($LASTEXITCODE -ne 0) { throw 'Safety stash failed' }
$MigrationStash = git rev-parse 'stash@{0}'
if (-not $MigrationStash) { throw 'Could not record safety stash hash' }
if (git status --porcelain=v1 -uall) { throw 'Worktree did not become clean; stop' }
git stash show --stat $MigrationStash
```

3. The inspection found no local `develop` branch, so create it directly from the freshly fetched remote-tracking ref, then apply the saved state without dropping the stash.

```powershell
git switch --create develop --track origin/develop
if ($LASTEXITCODE -ne 0) { throw 'Could not create develop; the safety stash remains intact' }
git stash apply $MigrationStash
if ($LASTEXITCODE -ne 0) {
    git diff --name-only --diff-filter=U
    throw 'Stash apply conflicted. Resolve each conflict before any deletion; do not use stash pop.'
}
if (git diff --name-only --diff-filter=U) { throw 'Unmerged paths remain' }
```

If `develop` is created by someone else before execution, do not use `-C` (it could reset their branch). After stashing, use `git switch develop`, require a clean branch, and run `git merge --ff-only origin/develop` instead.

4. Resolve and validate the exact deletion targets before recursively deleting them. Refuse reparse points/symlinks. This is the only destructive filesystem step.

```powershell
$RepoRoot = (Resolve-Path -LiteralPath '.').Path
$DeleteTargets = @(
    [IO.Path]::GetFullPath((Join-Path $RepoRoot '.claude')),
    [IO.Path]::GetFullPath((Join-Path $RepoRoot '.trellis\tasks\00-join-yuanjing'))
)
$ExpectedTargets = @(
    [IO.Path]::GetFullPath((Join-Path $RepoRoot '.claude')),
    [IO.Path]::GetFullPath((Join-Path $RepoRoot '.trellis\tasks\00-join-yuanjing'))
)
for ($i = 0; $i -lt $DeleteTargets.Count; $i++) {
    if (-not [string]::Equals($DeleteTargets[$i], $ExpectedTargets[$i], [StringComparison]::OrdinalIgnoreCase)) {
        throw "Unexpected delete target: $($DeleteTargets[$i])"
    }
    $Item = Get-Item -Force -LiteralPath $DeleteTargets[$i]
    if (($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing recursive deletion of reparse point: $($Item.FullName)"
    }
}
Remove-Item -Recurse -Force -LiteralPath $DeleteTargets[0]
Remove-Item -Recurse -Force -LiteralPath $DeleteTargets[1]
```

5. Verify the outcome and retain the safety stash until the migrated state is committed and independently checked.

```powershell
if ((git branch --show-current) -ne 'develop') { throw 'Not on develop' }
if (Test-Path -LiteralPath '.claude') { throw '.claude still exists' }
if (Test-Path -LiteralPath '.trellis\tasks\00-join-yuanjing') { throw 'Old task still exists' }
if (git diff --name-only --diff-filter=U) { throw 'Unmerged paths remain' }
git status --short --branch --untracked-files=all
git stash list
```

Expected result: `develop` tracks the freshly fetched `origin/develop`; all applied edits remain uncommitted; `.claude` appears as tracked deletions and has no leftover untracked children; the old untracked task directory is absent; all other tracked/untracked content came from the preserved stash. Do not drop `$MigrationStash` yet.

### Non-destructive Rollback

To restore the exact pre-migration state on `main`, first stash the entire attempted `develop` state so nothing from the attempt is lost, then apply the original stash by its recorded hash:

```powershell
git stash push --include-untracked --message 'safety: abandoned develop migration state'
$AttemptStash = git rev-parse 'stash@{0}'
git switch main
if ($LASTEXITCODE -ne 0) { throw 'Could not return to main; both safety stashes remain' }
git stash apply $MigrationStash
if ($LASTEXITCODE -ne 0) { throw 'Rollback apply needs conflict resolution; do not drop either stash' }
git status --short --branch --untracked-files=all
```

Because the original stash was made against this exact `main` commit, this restores the original modified/untracked `.claude` content and `00-join-yuanjing` along with every other captured change. The second stash preserves the attempted `develop` state. Only drop either stash after explicit verification.

### Code Patterns / Safety Rationale

- `git stash push --include-untracked` captures the currently visible tracked/untracked changes and produces a clean checkout boundary; `git stash apply <hash>` leaves the recovery object in place.
- A freshly fetched `origin/develop` plus `git switch --create develop --track origin/develop` prevents accidentally basing the work on stale local `main` or an old local `develop`.
- Avoid `git reset --hard`, `git clean`, forced branch reset (`git switch -C`), and `git stash pop`: each needlessly weakens recovery or risks unrelated files.
- Do not delete the two directories before creating the safety stash; doing so would make their untracked content unrecoverable through Git.
- Do not commit the safety snapshot on `main`; the task is specifically to move uncommitted work and the repository policy forbids developing directly on `main`.

### External References

- [Git stash documentation](https://git-scm.com/docs/git-stash) — `--include-untracked`, `apply`, stash commit references, and conflict behavior.
- [Git switch documentation](https://git-scm.com/docs/git-switch) — creation of a local tracking branch from a remote-tracking ref.
- [Git merge-base documentation](https://git-scm.com/docs/git-merge-base) — `--is-ancestor` precondition check.

### Related Specs

- `AGENTS.md` — requires normal work to start from latest `origin/develop`, prohibits direct development on `main`, requires PowerShell-first commands, and mandates resolved-path checks before recursive deletion.

## Caveats / Not Found

- No network fetch was performed during research, so the recorded `origin/develop` SHA is only the local remote-tracking state. The procedure fetches first.
- Git does not serialize against unrelated processes writing the worktree. Quiescing other writers is required for “all current changes” to mean one stable snapshot.
- Ignored files are not included by `stash -u`; they remain in place. None were found under the two deletion targets, and no ignored collision was found with paths newly tracked on the inspected `origin/develop`. If ignored data later appears inside `.claude` or the old task directory, the requested whole-directory deletion will remove it; back it up separately before proceeding if it must survive rollback.
- Applying onto newer `origin/develop` may expose additional overlaps after fetch. Stop on conflicts and merge deliberately; the retained stash remains the source of truth.
