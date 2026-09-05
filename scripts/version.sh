#!/usr/bin/env bash
#
# version.sh — CalVer bump, and optionally commit + tag + push.
#
# Format: 0.YYYYMM.MICRO (same month increments MICRO, a new month resets
# it to 1). Same scheme this org uses elsewhere (e.g. the private church
# repo), with one deliberate difference: MICRO is NOT zero-padded here.
# This repo is a real Go module meant to be `go get`-able, and Go's module
# resolver requires strict semver — golang.org/x/mod/semver rejects any
# numeric identifier with a leading zero, so a tag like v0.202609.001
# silently fails to resolve as a version at all (go get falls back to a
# pseudo-version and then can't verify it against the checksum database).
# v0.202609.1 is the same scheme, just valid semver.
#
# Usage:
#   ./scripts/version.sh --bump-only  # write the next version to VERSION
#                                     # (the per-PR bump `make version` runs)
#   ./scripts/version.sh              # bump, commit, tag vX, push (release)
#   ./scripts/version.sh --dry-run    # preview without making changes

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"

DRY_RUN=false
BUMP_ONLY=false
for arg in "$@"; do
    case "$arg" in
        --dry-run)   DRY_RUN=true ;;
        --bump-only) BUMP_ONLY=true ;;
        -h|--help)
            echo "Usage: $0 [--bump-only] [--dry-run]"
            echo ""
            echo "  --bump-only  Write the next version to VERSION and stop"
            echo "  --dry-run    Preview the next version without making changes"
            exit 0
            ;;
        *)
            echo "Unknown flag: $arg" >&2
            exit 1
            ;;
    esac
done

if [[ ! -f "$VERSION_FILE" ]]; then
    echo "Error: VERSION file not found at $VERSION_FILE" >&2
    exit 1
fi
CURRENT="$(tr -d '[:space:]' < "$VERSION_FILE")"

CURRENT_YYYYMM="$(echo "$CURRENT" | cut -d. -f2)"
CURRENT_MICRO="$(echo "$CURRENT" | cut -d. -f3)"
NOW_YYYYMM="$(date -u +%Y%m)"

if [[ "$NOW_YYYYMM" == "$CURRENT_YYYYMM" ]]; then
    NEXT_MICRO=$(( 10#$CURRENT_MICRO + 1 ))
    NEXT="0.${NOW_YYYYMM}.${NEXT_MICRO}"
else
    NEXT="0.${NOW_YYYYMM}.1"
fi

TAG="v${NEXT}"

echo "Current version: $CURRENT"
echo "Next version:    $NEXT"
if ! $BUMP_ONLY; then
    echo "Tag:             $TAG"
fi

if $DRY_RUN; then
    echo ""
    echo "(dry run — no changes made)"
    exit 0
fi

if $BUMP_ONLY; then
    echo "$NEXT" > "$VERSION_FILE"
    exit 0
fi

# Ensure we are on the main branch.
BRANCH="$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)"
if [[ "$BRANCH" != "main" ]]; then
    echo "Error: releases must be created from the main branch (currently on '$BRANCH')." >&2
    exit 1
fi

# Ensure working tree is clean (except VERSION).
if [[ -n "$(git -C "$REPO_ROOT" diff --name-only -- ':!VERSION')" ]]; then
    echo "Error: working tree has uncommitted changes. Commit or stash them first." >&2
    exit 1
fi

echo "$NEXT" > "$VERSION_FILE"

git -C "$REPO_ROOT" add VERSION
git -C "$REPO_ROOT" commit -m "chore: bump version to $NEXT"
git -C "$REPO_ROOT" tag -a "$TAG" -m "Release $TAG"

echo ""
echo "Created commit and tag $TAG"

git -C "$REPO_ROOT" push --follow-tags origin main
echo "Pushed to remote."
