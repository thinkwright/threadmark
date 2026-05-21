#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: scripts/prepare-release.sh vX.Y.Z

Prepare a Threadmark release locally:
  - require a clean worktree on main
  - stamp internal/buildinfo/version.go
  - stamp the GitHub Pages release badge
  - run release validation checks
  - commit the version stamp
  - create an annotated git tag

The script does not push. Push the release commit first, wait for CI, then push
the tag to trigger the release workflow.
USAGE
}

fail() {
  printf 'prepare-release: %s\n' "$*" >&2
  exit 1
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

tag="$1"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  fail "version must look like v0.1.0 or v0.1.0-rc.1"
fi
version="${tag#v}"

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "not inside a git worktree"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

branch="$(git branch --show-current)"
[[ "$branch" == "main" ]] || fail "release preparation must run on main, got ${branch:-detached HEAD}"

git diff --quiet || fail "working tree has unstaged changes"
git diff --cached --quiet || fail "index has staged changes"

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  fail "local tag $tag already exists"
fi

if git remote get-url origin >/dev/null 2>&1; then
  if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
    fail "remote tag origin/$tag already exists"
  fi
fi

version_file="internal/buildinfo/version.go"
[[ -f "$version_file" ]] || fail "missing $version_file"
landing_file="docs/index.html"
[[ -f "$landing_file" ]] || fail "missing $landing_file"

if ! grep -qE '^var Version = "[^"]+"$' "$version_file"; then
  fail "$version_file does not contain the expected Version assignment"
fi
if ! grep -qE '<strong data-threadmark-version>v[^<]+</strong>' "$landing_file"; then
  fail "$landing_file does not contain the expected release badge version marker"
fi
if ! grep -qE 'aria-label="Latest Threadmark release v[^"]+"' "$landing_file"; then
  fail "$landing_file does not contain the expected release badge aria label"
fi

sed -i.bak -E "s/^var Version = \"[^\"]+\"$/var Version = \"${version}\"/" "$version_file"
rm -f "$version_file.bak"
gofmt -w "$version_file"
sed -i.bak -E "s#<strong data-threadmark-version>v[^<]+</strong>#<strong data-threadmark-version>${tag}</strong>#" "$landing_file"
sed -i.bak -E "s#aria-label=\"Latest Threadmark release v[^\"]+\"#aria-label=\"Latest Threadmark release ${tag}\"#" "$landing_file"
rm -f "$landing_file.bak"

if ! grep -qx "var Version = \"${version}\"" "$version_file"; then
  fail "failed to stamp $version_file"
fi
if ! grep -q "<strong data-threadmark-version>${tag}</strong>" "$landing_file"; then
  fail "failed to stamp $landing_file release badge"
fi
if ! grep -q "aria-label=\"Latest Threadmark release ${tag}\"" "$landing_file"; then
  fail "failed to stamp $landing_file release badge aria label"
fi

if git diff --quiet -- "$version_file"; then
  fail "$version_file already contains version $version"
fi

printf 'Running release validation for %s...\n' "$tag"
go test ./...
go test -race ./...
go vet ./...
git diff --check
git ls-files '*.sh' | xargs -r bash -n

git add "$version_file" "$landing_file"
git commit -m "chore: release $tag"
git tag -a "$tag" -m "Threadmark $tag"

cat <<EOF
Prepared $tag.

Next steps:
  git push origin main
  # wait for CI on the release commit
  git push origin $tag

The tag push will trigger the GitHub release workflow.
EOF
