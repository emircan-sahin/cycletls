# @emircansahin/cycletls

[CycleTLS](https://github.com/Danny-Dasilva/CycleTLS) 2.0.5 with one fix, used by
[`@emircansahin/ghostfetch`](https://www.npmjs.com/package/@emircansahin/ghostfetch). The JavaScript
is upstream's 2.0.5 unchanged; the Go binaries are rebuilt from upstream commit `a96895f` (the 2.0.5
release) plus the patch below. The Go source they were built from is in `go/`. Licence: GPL-3.0, as
upstream.

## The fix

With `enableConnectionReuse`, each pooled client caches the first TLS connection per host
(`roundTripper.cachedConnections`) and never removes it. The HTTP/2 transport takes that connection,
and once it closes (the server or a proxy drops it, the network goes away) every new dial gets the same
closed connection back: each request to that host fails at once with
`use of closed network connection`, until a request to another host on the same client happens to
evict it.

`dialTLS` now hands the cached connection out once and deletes it, so the next dial opens a new one.
Reuse is unchanged: requests still share one connection while it is open.

```diff
 	if conn := rt.cachedConnections[addr]; conn != nil {
+		delete(rt.cachedConnections, addr)
 		return conn, nil
 	}
```

Tests: `go/cycletls/roundtripper_concurrency_test.go` (`TestDialTLS_HandsCachedConnOutOnce`) and
ghostfetch's `tests/connection-reuse.test.ts`, which fails on upstream 2.0.5 and passes on this build.

## Use

```ts
import initCycleTLS from '@emircansahin/cycletls';
```

Same API as `cycletls@2.0.5`. To rebuild: `fork/build.sh <out-dir>` on the
`fixes/2.0.5` branch of the fork.
