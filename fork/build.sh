#!/usr/bin/env bash
# Builds @emircansahin/cycletls: upstream cycletls@2.0.5 from npm with its seven Go binaries
# rebuilt from this branch (2.0.5 + the dialTLS fix), and the Go source they were built from.
# Usage: fork/build.sh <out-dir>   then: cd <out-dir>/package && npm publish --access public --tag latest
set -euo pipefail

UPSTREAM=2.0.5
VERSION=2.0.5-patch.1
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$(mkdir -p "$1" && cd "$1" && pwd)"
PKG="$OUT/package"

rm -rf "$PKG" "$OUT/cycletls-$UPSTREAM.tgz"
(cd "$OUT" && npm pack "cycletls@$UPSTREAM" --silent >/dev/null && tar xzf "cycletls-$UPSTREAM.tgz")

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
    description: "CycleTLS 2.0.5 with a fix for kept connections that close (a closed connection broke its host for good)",
    files: ["/dist", "/go"],
    license: "GPL-3.0",
  });
  delete pkg.scripts;
  delete pkg.devDependencies;
  fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
' "$PKG/package.json" "$VERSION"

echo "built $VERSION in $PKG"
