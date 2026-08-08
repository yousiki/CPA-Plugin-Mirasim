#!/usr/bin/env bash
#
# Builds a management panel that knows about Mirasim.
#
# The upstream panel compiles its quota providers in: QUOTA_ADAPTERS is a closed record
# and each filterFn matches one hard-coded provider string, so a plugin provider can
# never appear there without a panel change. This clones the pinned upstream release,
# applies that change, and produces a single self-contained management.html.
#
# Usage:  ./build.sh [output-path]
# Default output: dist/management.html next to this script.
set -euo pipefail

UPSTREAM_REPO="https://github.com/router-for-me/Cli-Proxy-API-Management-Center.git"
# Pinned deliberately: an unpinned build would silently drift from the patch below and
# fail to apply at the least convenient moment.
UPSTREAM_REF="v1.22.2"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
patch_file="$here/0001-mirasim-quota-adapter.patch"
out="${1:-$here/dist/management.html}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

command -v bun >/dev/null || { echo "bun is required (packageManager: bun@1.3.14)" >&2; exit 1; }

echo "==> cloning $UPSTREAM_REF"
git clone --quiet "$UPSTREAM_REPO" "$work/panel"
git -C "$work/panel" checkout --quiet "$UPSTREAM_REF"

echo "==> applying $(basename "$patch_file")"
git -C "$work/panel" apply --3way "$patch_file"

echo "==> installing dependencies"
(cd "$work/panel" && bun install --silent)

echo "==> verifying"
(cd "$work/panel" && bun run type-check && bun test)

# The patch hash goes into the version string so a deployed panel can be identified.
# Without it the only post-deploy signal is "does the word mirasim appear", which is true
# of every build including the broken ones — that is how a stale artifact once reached
# production and crashed the quota page.
patch_sha="$(shasum -a 256 "$patch_file" | cut -c1-8)"
build_version="${UPSTREAM_REF}+mirasim.${patch_sha}"

echo "==> building $build_version"
(cd "$work/panel" && VERSION="$build_version" bun run build)

mkdir -p "$(dirname "$out")"
cp "$work/panel/dist/index.html" "$out"
echo "==> wrote $out ($(wc -c <"$out" | tr -d ' ') bytes)"
echo "==> build id: $build_version"
echo "    verify a deployment with:"
echo "      curl -s <base>/management.html | grep -c '$build_version'"
cat <<'NOTE'

Install it by pointing CPA at the file and stopping it from replacing it:

  # docker-compose.yaml
  environment:
    MANAGEMENT_STATIC_PATH: /cpa/panel/management.html
  volumes:
    - ./panel:/cpa/panel

  # config.yaml
  remote-management:
    disable-auto-update-panel: true

Without the flag CPA compares the file's hash against the upstream release on startup
and overwrites it — the auto-updater writes to exactly the path above.
NOTE
