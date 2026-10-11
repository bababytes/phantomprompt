#!/bin/zsh
set -eu

cd -- "${0:A:h}"

if [[ -f go.mod ]] && command -v go >/dev/null 2>&1; then
  go build -o bin/phantomprompt ./cmd/phantomprompt
elif [[ ! -x bin/phantomprompt ]]; then
  if ! command -v go >/dev/null 2>&1; then
    print -u2 "Use a packaged release, or install Go to build from source."
    exit 1
  fi
  go build -o bin/phantomprompt ./cmd/phantomprompt
fi
exec ./bin/phantomprompt web
