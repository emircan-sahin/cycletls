#!/usr/bin/env bash
# Builds @emircansahin/cycletls: upstream cycletls@2.0.5 from npm with its seven Go binaries
# rebuilt from this branch (2.0.5 + the fixes in fork/README.md), one patched line in
# dist/index.js, and the Go source the binaries were built from.
# Usage: fork/build.sh <out-dir>   then: cd <out-dir>/package && npm publish --access public --tag latest
set -euo pipefail

UPSTREAM=2.0.5
VERSION=2.0.5-patch.2
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$(mkdir -p "$1" && cd "$1" && pwd)"
PKG="$OUT/package"

rm -rf "$PKG" "$OUT/cycletls-$UPSTREAM.tgz"
(cd "$OUT" && npm pack "cycletls@$UPSTREAM" --silent >/dev/null && tar xzf "cycletls-$UPSTREAM.tgz")

# Request ids: Date.now() plus a 0-999 random collided for two requests to one URL in the same ms
# (each got the other's frames). Same change as src/index.ts; fail if upstream's text moved.
node -e '
  const fs = require("fs");
  const file = process.argv[1];
  const from = "${this.clientId}#${url}#${Date.now()}-${Math.floor(1000 * Math.random())}";
  const to = "${this.clientId}#${url}#${require(\"crypto\").randomUUID()}";
  const source = fs.readFileSync(file, "utf8");
  if (source.split(from).length !== 2) {
    console.error("build.sh: request id expression not found exactly once in " + file);
    process.exit(1);
  }
  fs.writeFileSync(file, source.split(from).join(to));
' "$PKG/dist/index.js"

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
    description: "CycleTLS 2.0.5 with fixes for kept connections, WebSocket client leaks, a transport map race, stalled proxies and request id collisions",
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
