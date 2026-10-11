#!/bin/zsh
# Prepares a release: the README version badge, package.json/package-lock.json and CHANGELOG.md.
# It does not commit, tag or push; it prints those commands instead.
#   zsh scripts/release.sh 0.3.0
set -eu
cd -- "${0:A:h:h}"

release="${1:-}"
if [[ ! "${release}" =~ '^[0-9]+\.[0-9]+\.[0-9]+$' ]]; then
  print -u2 "usage: zsh scripts/release.sh MAJOR.MINOR.PATCH"
  exit 1
fi
if ! git diff --quiet || ! git diff --cached --quiet; then
  print -u2 "The working tree has uncommitted changes; commit or stash them first."
  exit 1
fi
if ! grep -qx '<!-- version:start -->' README.md || ! grep -qx '<!-- version:end -->' README.md; then
  print -u2 "README.md has no <!-- version:start --> / <!-- version:end --> markers."
  exit 1
fi
if ! awk '$0 == "<!-- version:start -->" { inside = 1; next } $0 == "<!-- version:end -->" { inside = 0 } inside' README.md \
  | grep -q 'img\.shields\.io/badge/release-v[0-9.]*-'; then
  print -u2 "README.md has no release badge between the version markers."
  exit 1
fi
if ! grep -qx '## \[Unreleased\]' CHANGELOG.md; then
  print -u2 "CHANGELOG.md has no ## [Unreleased] section."
  exit 1
fi
if grep -q "^## \[${release//./\\.}\]" CHANGELOG.md; then
  print -u2 "CHANGELOG.md already has a ## [${release}] section."
  exit 1
fi

npm version "${release}" --no-git-tag-version --allow-same-version >/dev/null

awk -v release="${release}" '
  $0 == "<!-- version:start -->" { inside = 1 }
  $0 == "<!-- version:end -->" { inside = 0 }
  inside { sub(/img\.shields\.io\/badge\/release-v[0-9.]+-/, "img.shields.io/badge/release-v" release "-") }
  { print }
' README.md > README.md.tmp
mv README.md.tmp README.md

awk -v heading="## [${release}] - $(date +%Y-%m-%d)" '
  !done && $0 == "## [Unreleased]" { print; print ""; print heading; done = 1; next }
  { print }
' CHANGELOG.md > CHANGELOG.md.tmp
mv CHANGELOG.md.tmp CHANGELOG.md

branch="$(git branch --show-current)"
print "Prepared v${release} (README.md, package.json, package-lock.json, CHANGELOG.md)."
print "Review the diff and the CHANGELOG entry, then run:"
print "  git commit -am \"Release v${release}\""
print "  git tag -a v${release} -m \"PhantomPrompt v${release}\""
print "  git push origin ${branch} v${release}"
