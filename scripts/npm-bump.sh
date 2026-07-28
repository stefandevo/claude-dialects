#!/bin/sh
set -eu

# Bump dashboard npm dependencies behind a minimum release age.
#
# npm resolves each requested spec against the registry as it stood at the
# cutoff, so a version published inside the cooldown window cannot enter
# package-lock.json. Versions already in the lockfile are left alone; the
# cutoff only constrains resolution of new ones.
#
# Usage:
#   ./scripts/npm-bump.sh jsdom@30.0.0
#   MIN_RELEASE_AGE_DAYS=0 ./scripts/npm-bump.sh jsdom@30.0.0   # bypass

root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
dashboard="$root/internal/app/dashboard"
days=${MIN_RELEASE_AGE_DAYS:-5}

if [ "$#" -eq 0 ]; then
  printf 'usage: %s <package@version> [package@version ...]\n' "$0" >&2
  exit 2
fi

case $days in
  '' | *[!0-9]*)
    printf 'MIN_RELEASE_AGE_DAYS must be a whole number of days, got: %s\n' "$days" >&2
    exit 2
    ;;
esac

# BSD date (macOS) first, GNU date second.
cutoff=$(date -u -v-"$days"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null) ||
  cutoff=$(date -u -d "$days days ago" +%Y-%m-%dT%H:%M:%SZ)

printf 'Resolving with a %s-day minimum release age (--before=%s)\n' "$days" "$cutoff" >&2
npm --prefix "$dashboard" install --save-exact --before="$cutoff" "$@"
