#!/usr/bin/env python3
"""Generate OhMyCine Player beta GitHub Release notes from git history."""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path


TAG_RE = re.compile(r"^v(?P<major>\d+)\.(?P<minor>\d+)\.(?P<patch>\d+)$")
COMMIT_RE = re.compile(r"^(?P<type>[A-Za-z]+)(?P<scope>\([^)]+\))?(?P<breaking>!)?:\s+.+$")
GROUP_ORDER = ("feat", "fix", "docs", "ci", "chore", "refactor", "test", "other")


def fail(message: str) -> None:
    print(f"::error::{message}", file=sys.stderr)
    raise SystemExit(1)


def git(args: list[str]) -> str:
    result = subprocess.run(
        ["git", *args],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip()
        fail(f"git {' '.join(args)} failed: {detail}")
    return result.stdout.strip()


def git_success(args: list[str]) -> bool:
    return (
        subprocess.run(
            ["git", *args],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            text=True,
        ).returncode
        == 0
    )


def parse_tag(tag_name: str) -> tuple[int, int, int]:
    match = TAG_RE.fullmatch(tag_name)
    if not match:
        fail("TAG_NAME must look like v0.0.1.")
    return tuple(int(match.group(part)) for part in ("major", "minor", "patch"))


def resolve_commit(ref: str) -> str:
    return git(["rev-parse", "--verify", f"{ref}^{{commit}}"])


def find_previous_tag(tag_name: str, current_commit: str) -> str | None:
    current_version = parse_tag(tag_name)
    tags = git(["tag", "--list", "v[0-9]*.[0-9]*.[0-9]*", "--sort=-v:refname"]).splitlines()

    for candidate in tags:
        candidate = candidate.strip()
        if not TAG_RE.fullmatch(candidate) or candidate == tag_name:
            continue
        if parse_tag(candidate) >= current_version:
            continue
        if git_success(["merge-base", "--is-ancestor", f"{candidate}^{{commit}}", current_commit]):
            return candidate

    return None


def read_commits(revision: str) -> list[tuple[str, str]]:
    output = git(["log", "--reverse", "--format=%H%x1f%s", revision])
    commits: list[tuple[str, str]] = []

    for line in output.splitlines():
        if not line:
            continue
        sha, _, subject = line.partition("\x1f")
        subject = " ".join(subject.replace("\r", " ").replace("\n", " ").split())
        commits.append((sha[:7], subject))

    return commits


def group_for_subject(subject: str) -> str:
    match = COMMIT_RE.match(subject)
    if not match:
        return "other"

    commit_type = match.group("type").lower()
    if commit_type in GROUP_ORDER and commit_type != "other":
        return commit_type
    return "other"


def build_markdown(
    *,
    tag_name: str,
    previous_tag: str | None,
    commits: list[tuple[str, str]],
    extra_notes: str,
    workflow_event_name: str,
    asset_prefix: str,
) -> str:
    grouped: dict[str, list[tuple[str, str]]] = {group: [] for group in GROUP_ORDER}
    for sha, subject in commits:
        grouped[group_for_subject(subject)].append((sha, subject))

    range_label = f"{previous_tag}..{tag_name}" if previous_tag else f"initial commit..{tag_name}"
    lines = [
        f"# OhMyCine Player {tag_name} Beta",
        "",
        "OhMyCine Player beta prerelease.",
        "",
        "## Version Rule",
        "",
        "`vMAJOR.MINOR.BETA` is used for Player beta releases. The first two numbers identify the product stage, and the last number is the beta iteration. Player app files use the same version without the leading `v`.",
        "",
        "## Changes",
        "",
        f"Range: `{range_label}`.",
        "",
    ]

    if commits:
        for group in GROUP_ORDER:
            entries = grouped[group]
            if not entries:
                continue
            lines.append(f"### {group}")
            lines.append("")
            for sha, subject in entries:
                lines.append(f"- {subject} ({sha})")
            lines.append("")
    else:
        lines.append("No commits found in this range.")
        lines.append("")

    if workflow_event_name == "workflow_dispatch" and extra_notes.strip():
        lines.extend(
            [
                "## Extra Notes",
                "",
                extra_notes.rstrip(),
                "",
            ]
        )

    lines.extend(
        [
            "## Assets",
            "",
            f"- Windows x64 NSIS installer: `{asset_prefix}-setup.exe`",
            f"- Windows x64 portable zip: `{asset_prefix}-portable.zip`",
            f"- SHA-256 checksums: `{asset_prefix}.sha256`",
            "",
            "## Checksums",
            "",
            "The `.sha256` file is generated with `sha256sum` and contains checksums for the installer and portable zip.",
        ]
    )

    return "\n".join(lines).rstrip() + "\n"


def main() -> None:
    tag_name = os.environ.get("TAG_NAME", "").strip()
    if not tag_name:
        fail("TAG_NAME is required.")
    parse_tag(tag_name)

    if git_success(["rev-parse", "--verify", f"{tag_name}^{{commit}}"]):
        current_commit = resolve_commit(tag_name)
    else:
        current_commit = resolve_commit(os.environ.get("CURRENT_REF", "HEAD"))

    previous_tag = find_previous_tag(tag_name, current_commit)
    revision = f"{previous_tag}..{current_commit}" if previous_tag else current_commit
    commits = read_commits(revision)

    asset_prefix = os.environ.get("ASSET_PREFIX", f"OhMyCine-Player-{tag_name}-windows-x64").strip()
    output_path = Path(os.environ.get("OUTPUT_PATH", "dist/player-beta/release-notes.md"))
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(
        build_markdown(
            tag_name=tag_name,
            previous_tag=previous_tag,
            commits=commits,
            extra_notes=os.environ.get("EXTRA_RELEASE_NOTES", ""),
            workflow_event_name=os.environ.get("WORKFLOW_EVENT_NAME", ""),
            asset_prefix=asset_prefix,
        ),
        encoding="utf-8",
    )

    print(f"Wrote release notes to {output_path}")


if __name__ == "__main__":
    main()
