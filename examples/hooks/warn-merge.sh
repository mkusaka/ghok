#!/bin/sh
# Read-only advisory example. Requires jq and gh; install as a trusted pre-hook.
# gh inherits ghok's verified nested token, so this lookup does not recurse.
set -eu
input=$(cat)
warning=$(printf '%s' "$input" | jq -r '
  if any(.operations[]?; .parameters.admin == true) then
    "Admin merge requested; check the normal review workflow."
  else "" end')
target=$(printf '%s' "$input" | jq -r '
  first(.operations[]? | select(.execution != "skipped" and .execution != "not-selected")
    | select(.target.repository != null and .target.number != null)
    | [.target.host // "github.com", .target.repository, (.target.number | tostring)] | @tsv) // ""')
if [ -n "$target" ]; then
  host=$(printf '%s' "$target" | cut -f1)
  repo=$(printf '%s' "$target" | cut -f2)
  number=$(printf '%s' "$target" | cut -f3)
  # No POST/PATCH/DELETE or field flags: this is a GET only. Failure is diagnostic,
  # not a denial; onError in the config controls actual hook execution failures.
  if labels=$(gh api --hostname "$host" --method GET "repos/$repo/issues/$number/labels" 2>/dev/null); then
    if printf '%s' "$labels" | jq -e 'any(.[]; .name == "release")' >/dev/null; then
      warning="${warning}${warning:+
}Release-labeled PR: verify the release checklist."
    fi
  else
    printf '%s\n' 'Label lookup unavailable; advisory uses local information only.' >&2
  fi
fi
hour=$(date +%H)
case "$hour" in
  0[0-8]|1[89]|2[0-3]) warning="${warning}${warning:+
}Outside local working hours; check release coverage." ;;
esac
jq -n --arg warning "$warning" '{schemaVersion:1,decision:"abstain"} +
  (if $warning == "" then {} else {warning:$warning} end)'
