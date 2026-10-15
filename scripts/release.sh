#!/usr/bin/env bash
# Cut a release: move CHANGELOG.md's [Unreleased] section to the new version,
# commit, tag and push. The release workflow then publishes the GitHub release
# with that section on top.
# Usage: scripts/release.sh patch|minor|major|vX.Y.Z
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

arg="${1:-}"
if [[ -z "$arg" ]]; then
  echo "Usage: $0 <patch|minor|major|vX.Y.Z>"
  echo "Recent tags:"; git tag --sort=-version:refname | head -5
  exit 1
fi

git fetch --tags origin
latest=$(git tag --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
latest=${latest:-v0.0.0}
if [[ "$arg" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  tag="$arg"
else
  IFS=. read -r major minor patch <<<"${latest#v}"
  case "$arg" in
    patch) patch=$((patch + 1)) ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    major) major=$((major + 1)); minor=0; patch=0 ;;
    *) echo "Error: expected patch|minor|major|vX.Y.Z, got '$arg'"; exit 1 ;;
  esac
  tag="v${major}.${minor}.${patch}"
fi

[[ "$(git branch --show-current)" == main ]] || { echo "Error: release from main"; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo "Error: working tree is not clean"; git status --short; exit 1; }
git fetch origin main
[[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || { echo "Error: main is not in sync with origin/main"; exit 1; }
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null || git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  echo "Error: tag '$tag' already exists"; exit 1
fi

notes=$(scripts/changelog-section.sh Unreleased)
if ! grep -q '[^[:space:]#]' <<<"$notes"; then
  echo "Error: CHANGELOG.md [Unreleased] is empty; describe the changes first"; exit 1
fi

echo "$latest -> $tag"
echo "Commit: $(git log --oneline -1)"
echo "----- release notes -----"
echo "$notes"
echo "-------------------------"
read -rp "Proceed? [y/N] " confirm
[[ "$confirm" == [yY] ]] || { echo "Aborted."; exit 0; }

today=$(date -u +%Y-%m-%d)
# Rename [Unreleased] to the new version, open a fresh [Unreleased], fix compare links.
awk -v tag="$tag" -v today="$today" -v prev="$latest" '
  !done && /^## \[Unreleased\]/ { print "## [Unreleased]"; print ""; print "## [" tag "] - " today; done = 1; next }
  /^\[Unreleased\]: / { print "[Unreleased]: https://github.com/mkusaka/ghok/compare/" tag "...HEAD"; print "[" tag "]: https://github.com/mkusaka/ghok/compare/" prev "..." tag; next }
  { print }
' CHANGELOG.md > CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md

git add CHANGELOG.md
git commit -m "release $tag"
git push origin main
git tag -a "$tag" -m "$tag"
git push origin "$tag"
echo "Pushed $tag. Watch: gh run list --workflow Release --limit 1"
