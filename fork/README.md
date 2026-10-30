# @emircansahin/cycletls

[CycleTLS](https://github.com/Danny-Dasilva/CycleTLS) 2.0.5 with the fixes below, used by
[`@emircansahin/ghostfetch`](https://www.npmjs.com/package/@emircansahin/ghostfetch). The JavaScript
is upstream's 2.0.5 with one line changed (the request id, fix 7); the Go binaries are rebuilt from
upstream commit `a96895f` (the 2.0.5 release) plus the patches below. The Go source they were built
from is in `go/`. Licence: GPL-3.0, as upstream.

## The fixes

### 1. A closed kept connection broke its host for good (patch.1)

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

### 2. Every disconnected client leaked a socket and its goroutines (patch.2)

When the Node client went away without sending `exit`, `readSocket` returned without closing the
socket and `writeSocket` waited on its channel forever: one fd (CLOSED in `lsof`) and its
goroutines per client, for the life of the process. `readSocket` now closes the socket and its
channels on the way out; `writeSocket` drains without writing once the client is gone and returns
when `readProcess` closes the write channel after the last in-flight request. The `exit` close frame
goes through `WriteControl`, which is safe next to `writeSocket`'s writes.

### 3. One invalid message ended a client's request loop (patch.2)

A message that did not parse as JSON made `readSocket` return, leaving the socket open but
answering nothing. It is now logged and skipped.

### 4. A concurrent map read and write could kill the process (patch.2)

`RoundTrip` read `cachedTransports` without a lock while dials wrote it; Go aborts the whole
process on that. The map now has its own `RWMutex` (not the roundTripper's lock, which `dialTLS`
holds across network I/O). The TLS 1.3 and TLS 1.2 retry paths also replaced a working transport on
a redial and failed the live request with `protocol negotiated`; like the main path, they now hand
the connection to the existing transport.

### 5. A stalled proxy or server blocked every request on the client (patch.2)

`dialTLS` holds the roundTripper's lock across the proxy CONNECT and the TLS handshake, neither of
which had a deadline, and HTTP/2 redials pass `context.Background()`. One proxy that accepted and
went silent froze the client. Both now run under the dial context's deadline, or the request
timeout (`timeout`, 15 s by default) when it has none, cleared on success.

### 6. Dead HTTP/2 connections hung requests until their timeout (patch.2)

A kept HTTP/2 connection that died without a FIN (NAT or proxy drop) stayed in the pool. The
HTTP/2 transports now ping after 30 s without reads (`ReadIdleTimeout`) and drop the connection
when the ping gets no answer in 15 s (`PingTimeout`).

### 7. Request ids collided (patch.2)

The JavaScript client built ids as the URL plus `Date.now()` and a 0-999 random, so two requests to
one URL in the same millisecond collided 1 time in 1000 and got each other's frames. The id now
ends in `crypto.randomUUID()`. `fork/build.sh` patches that one expression in upstream's
`dist/index.js` and fails if the text is not there; `src/index.ts` has the same change.

Tests for 2-5: `go/cycletls/index_websocket_test.go` and
`go/cycletls/roundtripper_concurrency_test.go`; each fails on upstream 2.0.5 and passes here
(`go test -race`).

## Use

```ts
import initCycleTLS from '@emircansahin/cycletls';
```

Same API as `cycletls@2.0.5`. To rebuild: `fork/build.sh <out-dir>` on the
`fixes/2.0.5` branch of the fork.
