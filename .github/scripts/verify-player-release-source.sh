#!/usr/bin/env bash

set -euo pipefail

release_channel="${1:-}"
release_ref="${2:-HEAD}"
workflow_event_name="${3:-}"
workflow_ref="${4:-}"

case "$release_channel" in
  beta)
    source_branch="develop"
    ;;
  stable)
    source_branch="main"
    ;;
  *)
    echo "::error::Release channel must be beta or stable."
    exit 1
    ;;
esac

case "$workflow_event_name" in
  workflow_dispatch)
    expected_ref="refs/heads/${source_branch}"
    if [[ "$workflow_ref" != "$expected_ref" ]]; then
      echo "::error::Manual ${release_channel} releases must be dispatched from ${expected_ref}, not ${workflow_ref:-<empty>}."
      exit 1
    fi
    ;;
  push)
    if [[ "$release_channel" != "beta" ]] || [[ ! "$workflow_ref" =~ ^refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "::error::Tag pushes must use the beta channel and a vMAJOR.MINOR.PATCH tag ref."
      exit 1
    fi
    ;;
  *)
    echo "::error::Player releases require a workflow_dispatch or tag push event."
    exit 1
    ;;
esac

git fetch origin "refs/heads/${source_branch}" --no-tags
release_sha="$(git rev-parse "${release_ref}^{commit}")"
source_sha="$(git rev-parse 'FETCH_HEAD^{commit}')"

if [[ "$release_sha" != "$source_sha" ]]; then
  echo "::error::Player ${release_channel} releases must use the latest remote ${source_branch} commit. Release ${release_sha}, origin/${source_branch} ${source_sha}."
  exit 1
fi

echo "Player ${release_channel} release source verified: ${release_sha} equals origin/${source_branch}."
