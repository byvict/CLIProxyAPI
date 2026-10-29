#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 || "$1" != /* || "$2" != /* ]]; then
  echo 'Usage: scripts/build-management.sh /absolute/build-root /absolute/management.html' >&2
  exit 2
fi
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$1"
build_dir="$(mktemp -d "$1/management-XXXXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
upstream_repo="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["repository"])' "$repo_root/management/upstream.json")"
upstream_commit="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["commit"])' "$repo_root/management/upstream.json")"
git -C "$build_dir" init -q
git -C "$build_dir" fetch --depth 1 "$upstream_repo" "$upstream_commit"
git -C "$build_dir" checkout -q --detach FETCH_HEAD
git -C "$build_dir" apply "$repo_root/management/refinements.patch"
cp -R "$repo_root/management/src/." "$build_dir/src/"
cp -R "$repo_root/management/tests/." "$build_dir/tests/"
python3 "$repo_root/scripts/build-v1-history.py" "$build_dir"
cd "$build_dir"
bun install --frozen-lockfile
bun run verify
mkdir -p "$(dirname "$2")"
cp dist/index.html "$2"
echo "Management artifact: $2"
