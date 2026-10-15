#!/usr/bin/env bash
# Print the CHANGELOG.md body of one version section (without its heading).
# Usage: scripts/changelog-section.sh vX.Y.Z|Unreleased [CHANGELOG.md]
set -euo pipefail
version="${1:?usage: $0 vX.Y.Z|Unreleased [file]}"
file="${2:-CHANGELOG.md}"
awk -v want="[${version}]" '
  /^## \[/ { if (found) exit; found = (index($0, "## " want) == 1); next }
  /^\[[^]]+\]: / { if (found) exit }
  found { print }
' "$file" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
