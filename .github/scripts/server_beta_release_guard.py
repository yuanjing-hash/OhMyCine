#!/usr/bin/env python3
"""Resolve and statically verify the Server-only beta release contract."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


VERSION_RE = re.compile(r"^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_WORKFLOW = ROOT / ".github" / "workflows" / "server-beta-release.yml"
DEFAULT_PLAYER_WORKFLOW = ROOT / ".github" / "workflows" / "player-beta-release.yml"


def normalize_version(raw: str) -> tuple[str, str]:
    candidate = raw.strip()
    match = VERSION_RE.fullmatch(candidate)
    if match is None:
        raise ValueError("version must be strict semantic version X.Y.Z or vX.Y.Z")
    version = ".".join(match.groups())
    return version, f"server-v{version}"


def push_tag_patterns(path: Path) -> list[str]:
    patterns: list[str] = []
    in_tags = False
    for line in path.read_text(encoding="utf-8").splitlines():
        indent = len(line) - len(line.lstrip())
        stripped = line.strip()
        if indent == 4 and stripped == "tags:":
            in_tags = True
            continue
        if in_tags and indent <= 4 and stripped:
            break
        if in_tags and indent == 6 and stripped.startswith("-"):
            patterns.append(stripped[1:].strip().strip("\"'"))
    return patterns


def verify_workflow(
    path: Path = DEFAULT_WORKFLOW, player_path: Path = DEFAULT_PLAYER_WORKFLOW
) -> list[str]:
    text = path.read_text(encoding="utf-8")
    player_tag_patterns = push_tag_patterns(player_path)
    required = {
        "manual dispatch only": "workflow_dispatch:" in text and "\n  push:" not in text,
        "normalized namespaced tag": "server_beta_release_guard.py resolve-version" in text,
        "Player tag trigger excludes Server namespace": bool(player_tag_patterns)
        and all(pattern.startswith("v") for pattern in player_tag_patterns),
        "concurrency uses normalized tag": "group: server-release-${{ needs.resolve.outputs.tag_name }}" in text,
        "develop source is checked twice": text.count("git fetch --force --prune origin develop") >= 2
        and text.count("refs/remotes/origin/develop") >= 2,
        "existing tags are commit checked": text.count('refs/tags/${TAG_NAME}^{commit}') >= 2,
        "missing release creates namespaced prerelease": 'gh release create "${create_args[@]}"' in text
        and "--prerelease" in text,
        "release identity and title are checked": text.count(
            "--json tagName,name,isPrerelease,isDraft"
        )
        >= 2
        and text.count("OhMyCine Server v${VERSION} Beta") >= 2,
        "only official read token secret is injected": text.count(
            "secrets.OHMYCINE_TMDB_READ_ACCESS_TOKEN"
        )
        == 1,
        "embedded webui is mandatory": text.count("go build -tags webui") == 2,
        "windows archive is built": "windows-x64.zip" in text,
        "linux archive is built": "linux-x64.tar.gz" in text,
        "checksum manifest is built": "SHA256SUMS.txt" in text and "sha256sum" in text,
        "webui gates run": all(
            command in text
            for command in (
                "npm run permissions:check",
                "npm run test",
                "npm run typecheck",
                "npm run lint",
                "npm run build",
            )
        ),
        "go gates run": all(
            command in text
            for command in (
                "go mod verify",
                "go build ./...",
                "go vet ./...",
                "go test ./...",
                "golangci-lint-action",
            )
        ),
        "lint action supports v2 and version is pinned": "golangci/golangci-lint-action@v7" in text and "version: v2.4.0" in text and "version: latest" not in text,
        "idempotent asset upload": 'gh release upload "$TAG_NAME"' in text and "--clobber" in text,
        "publish follows packaging": text.find("Build and package embedded-WebUI Server archives")
        < text.find("Revalidate latest develop and publish Server-only prerelease"),
        "job write permission is scoped": "permissions:\n      contents: write" in text,
    }
    forbidden = {
        "Player release dependency": "Existing Player beta release",
        "Player tag namespace": "refs/tags/v${",
        "legacy API-key build secret": "OHMYCINE_TMDB_API_KEY",
        "Player asset path": "player/",
    }

    failures = [label for label, passed in required.items() if not passed]
    failures.extend(label for label, token in forbidden.items() if token in text)
    return failures


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    version_parser = subparsers.add_parser("resolve-version")
    version_parser.add_argument("version")
    workflow_parser = subparsers.add_parser("check-workflow")
    workflow_parser.add_argument("path", nargs="?", type=Path, default=DEFAULT_WORKFLOW)
    args = parser.parse_args(argv)

    if args.command == "resolve-version":
        try:
            version, tag_name = normalize_version(args.version)
        except ValueError as exc:
            print(f"::error::{exc}", file=sys.stderr)
            return 2
        print(f"version={version}")
        print(f"tag_name={tag_name}")
        return 0

    failures = verify_workflow(args.path)
    if failures:
        for failure in failures:
            print(f"::error::Server beta release guard failed: {failure}", file=sys.stderr)
        return 1
    print("Server-only beta release workflow contract verified.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
