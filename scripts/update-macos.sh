#!/bin/zsh
set -eu
cd -- "${0:A:h:h}"

if [[ -d node_modules ]]; then
  npm run --silent build:web
fi
go build -trimpath -o bin/phantomprompt ./cmd/phantomprompt
zsh scripts/package-macos.sh
bin/phantomprompt stop
ditto dist/PhantomPrompt.app /Applications/PhantomPrompt.app
open /Applications/PhantomPrompt.app
print "PhantomPrompt updated in /Applications and started."
print "Terminals protected before the update: run exit, then phantom again."
