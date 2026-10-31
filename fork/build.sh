#!/usr/bin/env bash
# Builds @emircansahin/cycletls: upstream cycletls@2.0.5's npm package with its JavaScript compiled
# from this branch's src/index.ts and its seven Go binaries rebuilt from this branch (2.0.5 + the
# fixes in fork/README.md), plus the Go source the binaries were built from.
# Usage: fork/build.sh <out-dir>   then: cd <out-dir>/package && npm publish --access public --tag latest
set -euo pipefail

UPSTREAM=2.0.5
VERSION=2.0.5-patch.4
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$(mkdir -p "$1" && cd "$1" && pwd)"
PKG="$OUT/package"

rm -rf "$PKG" "$OUT/cycletls-$UPSTREAM.tgz"
(cd "$OUT" && npm pack "cycletls@$UPSTREAM" --silent >/dev/null && tar xzf "cycletls-$UPSTREAM.tgz")

# The JavaScript from src/index.ts. Upstream's a96895f source compiles with this tsconfig to the
# same dist/index.js npm ships (checked 2026-10-31), so the fixes are the only difference.
TSC="$ROOT/node_modules/.bin/tsc"
[ -x "$TSC" ] || { echo "build.sh: run npm install in $ROOT first (tsc)" >&2; exit 1; }
"$TSC" -p "$ROOT/tsconfig.json" --outDir "$PKG/dist" --declarationDir "$PKG/dist"

# Pinned: the stdlib's crypto/x509 is what utls verifies certificates with, and patch.3's go1.26.1
# missed its fixes (GO-2026-4946, -4947, -6809). The go command fetches this toolchain if needed.
export GOTOOLCHAIN=go1.26.10

# Same targets and file names as upstream's build:go scripts, so its dist/index.js finds them.
targets=(
  "linux amd64 index" "linux arm index-arm" "linux arm64 index-arm64"
  "darwin amd64 index-mac" "darwin arm64 index-mac-arm64"
  "windows amd64 index.exe" "freebsd amd64 index-freebsd"
)
for target in "${targets[@]}"; do
  read -r goos goarch file <<<"$target"
  (cd "$ROOT/src" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOFLAGS=-mod=mod \
    go build -trimpath -o "$PKG/dist/$file" ./)
done

# GPL-3.0: the corresponding source ships with the binaries.
mkdir -p "$PKG/go"
cp -R "$ROOT/cycletls" "$ROOT/src" "$PKG/go/"
rm -rf "$PKG/go/cycletls/tests" "$PKG/go/src/index.ts"
cp "$ROOT/fork/README.md" "$PKG/README.md"

node -e '
  const fs = require("fs");
  const file = process.argv[1];
  const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
  Object.assign(pkg, {
    name: "@emircansahin/cycletls",
    version: process.argv[2],
    description: "CycleTLS 2.0.5 with fixes for a dead Go process hanging requests, per-request crashes, kept connections, WebSocket client leaks, a transport map race, stalled proxies and request id collisions",
    files: ["/dist", "/go"],
    license: "GPL-3.0",
    repository: { type: "git", url: "git+https://github.com/emircan-sahin/cycletls.git", directory: "fork" },
    bugs: { url: "https://github.com/emircan-sahin/cycletls/issues" },
    homepage: "https://github.com/emircan-sahin/cycletls/tree/fixes/2.0.5/fork#readme",
  });
  delete pkg.scripts;
  delete pkg.devDependencies;
  fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
' "$PKG/package.json" "$VERSION"

echo "built $VERSION in $PKG"
