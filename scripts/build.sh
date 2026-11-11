#!/bin/sh
# Puts ./bin/herdr-linear-kickoff in place. herdr runs this as the manifest's
# [[build]] step after `herdr plugin install`; run it by hand after changing
# the code (`herdr plugin link` doesn't build).
#
# With Go 1.25+ it builds from source. Without it, it downloads the release
# binary for this platform and version (herdr-plugin.toml) from GitHub and
# checks its SHA-256 against the release's checksums.txt before using it.
#
#   LK_FORCE_DOWNLOAD=1   use the release binary even with Go installed
#   LK_RELEASE_BASE=URL   download from a mirror instead of GitHub Releases
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin

REPO=rshelekhov/herdr-linear-kickoff
NAME=herdr-linear-kickoff

say() { echo "herdr-linear-kickoff: $*" >&2; }

# Go 1.25 or newer?
go_ok() {
	command -v go >/dev/null 2>&1 || return 1
	v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
	major=${v%%.*}
	rest=${v#*.}
	minor=${rest%%.*}
	[ "${major:-0}" -gt 1 ] || { [ "${major:-0}" -eq 1 ] && [ "${minor:-0}" -ge 25 ]; }
}

if [ "${LK_FORCE_DOWNLOAD:-}" != 1 ] && go_ok; then
	exec go build -trimpath -ldflags "-s -w" -o "bin/$NAME" .
fi

version=$(sed -n 's/^version = "\(.*\)"/\1/p' herdr-plugin.toml)
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*)
	say "no release binary for $(uname -m); install Go 1.25+ (https://go.dev/dl) and retry"
	exit 1
	;;
esac
case "$os" in
darwin | linux) ;;
*)
	say "no release binary for $os; install Go 1.25+ (https://go.dev/dl) and retry"
	exit 1
	;;
esac

archive="${NAME}_${version}_${os}_${arch}.tar.gz"
base="${LK_RELEASE_BASE:-https://github.com/$REPO/releases/download/v$version}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "Go 1.25+ not found — downloading the v$version release for $os/$arch"
if ! curl -fsSL -o "$tmp/$archive" "$base/$archive" || ! curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"; then
	say "download failed ($base/$archive); install Go 1.25+ (https://go.dev/dl) to build from source"
	exit 1
fi

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
	say "checksum mismatch for $archive — not installing it"
	exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp" "$NAME"
mv "$tmp/$NAME" "bin/$NAME"
chmod 755 "bin/$NAME"
say "installed bin/$NAME v$version"
