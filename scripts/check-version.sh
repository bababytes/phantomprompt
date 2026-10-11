#!/bin/zsh
# Checks that a release tag matches the README version badge, package.json and CHANGELOG.md, and, in
# GitHub Actions, that the README links point at the repository the release is built in.
# Used by the release workflow before anything is built.
#   zsh scripts/check-version.sh v0.3.0
set -eu
cd -- "${0:A:h:h}"

tag="${1:-}"
if [[ ! "${tag}" =~ '^v[0-9]+\.[0-9]+\.[0-9]+$' ]]; then
  print -u2 "usage: zsh scripts/check-version.sh vMAJOR.MINOR.PATCH"
  exit 1
fi
release="${tag#v}"

readme="$(awk '$0 == "<!-- version:start -->" { inside = 1; next } $0 == "<!-- version:end -->" { inside = 0 } inside' README.md \
  | sed -n 's|.*img.shields.io/badge/release-v\([0-9][0-9.]*\)-.*|\1|p')"
package="$(node -p 'require("./package.json").version')"

problems=()
[[ "${readme}" == "${release}" ]] || problems+=("README.md badge: ${readme:-missing}")
[[ "${package}" == "${release}" ]] || problems+=("package.json version: ${package:-missing}")
grep -q "^## \[${release//./\\.}\]" CHANGELOG.md || problems+=("CHANGELOG.md: no ## [${release}] section")
if [[ -n "${GITHUB_REPOSITORY:-}" ]]; then
  for repository in ${(u)${(f)"$(grep -o 'github\.com/[A-Za-z0-9_.-]*/[A-Za-z0-9_.-]*' README.md | cut -d/ -f2-3)"}}; do
    [[ "${(L)repository}" == "${(L)GITHUB_REPOSITORY}" ]] || problems+=("README.md links to ${repository}, the release is built in ${GITHUB_REPOSITORY}")
  done
fi

if (( ${#problems} )); then
  print -u2 "Tag ${tag} does not match:"
  for problem in "${problems[@]}"; do print -u2 "  ${problem}"; done
  exit 1
fi
print "${tag} matches README.md, package.json and CHANGELOG.md."
