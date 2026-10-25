#!/usr/bin/env bash
# Push HEAD to main only after `make ci` passes on exactly that commit, on top
# of the current origin/main. A plain push already refuses non-fast-forwards;
# this also refuses to push a commit that was never checked as integrated.
# Usage: scripts/push-main.sh
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

clean() { [[ -z "$(git status --porcelain)" ]]; }
contains_main() {
  git fetch -q origin main
  git merge-base --is-ancestor origin/main HEAD || {
    echo "Error: HEAD does not contain origin/main; rebase or merge origin/main, then rerun"; exit 1
  }
}

clean || { echo "Error: working tree is not clean"; git status --short; exit 1; }
head=$(git rev-parse HEAD)
contains_main
[[ "$head" != "$(git rev-parse origin/main)" ]] || { echo "origin/main is already at $head"; exit 0; }
git log --oneline origin/main..HEAD

make ci

[[ "$(git rev-parse HEAD)" == "$head" ]] && clean || { echo "Error: HEAD or the working tree changed during make ci"; exit 1; }
contains_main
git push origin "$head:refs/heads/main"
