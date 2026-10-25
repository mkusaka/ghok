#!/bin/sh
set -e
cd "$(dirname "$0")"
for f in Policy Match Config Hooks Decide; do rocq compile -Q . Ghok $f.v; done
