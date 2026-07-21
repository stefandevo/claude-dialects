#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
output="$root/THIRD_PARTY_NOTICES.md"
temp=$(mktemp)
clean=$(mktemp)
trap 'rm -f "$temp" "$clean"' EXIT

{
  printf '# Third-party notices\n\n'
  printf 'This file is generated from the Go modules compiled into Claude Dialects and the npm packages bundled into its embedded dashboard.\n'
  printf "Run \`./scripts/generate-third-party-notices.sh\` after dependency changes.\n"
} > "$temp"

go list -deps -f '{{with .Module}}{{if ne .Path "github.com/stefandevo/claude-dialects"}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}' ./... |
  sed '/^$/d' |
  sort -u |
  while IFS='|' read -r module version directory; do
    license_files=$(find "$directory" -maxdepth 1 -type f \( \
      -iname 'LICENSE*' -o \
      -iname 'COPYING*' -o \
      -iname 'NOTICE*' \
    \) | sort)

    if [ -z "$license_files" ]; then
      printf 'No license file found for %s %s\n' "$module" "$version" >&2
      exit 1
    fi

    {
      printf '\n## %s %s\n' "$module" "$version"
      printf '\nSource: https://%s\n' "$module"
    } >> "$temp"

    printf '%s\n' "$license_files" |
      while IFS= read -r license_file; do
        {
          printf '\n### %s\n\n```text\n' "$(basename "$license_file")"
          cat "$license_file"
          printf '\n```\n'
        } >> "$temp"
      done
  done

node "$root/scripts/generate-frontend-notices.mjs" >> "$temp"

sed 's/[[:space:]]*$//' "$temp" > "$clean"
mv "$clean" "$output"
rm -f "$temp"
trap - EXIT
