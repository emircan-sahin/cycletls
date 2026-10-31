# @emircansahin/cycletls

[CycleTLS](https://github.com/Danny-Dasilva/CycleTLS) 2.0.5 with the fixes below, used by
[`@emircansahin/ghostfetch`](https://www.npmjs.com/package/@emircansahin/ghostfetch). Both halves are
built from upstream commit `a96895f` (the 2.0.5 release) plus the patches below: the JavaScript is
compiled from `src/index.ts` (upstream's source compiles to the exact `dist/index.js` npm ships, so
the fixes are the only difference), and the Go binaries from `src/` and `cycletls/`. The Go source
is in `go/`. Licence: GPL-3.0, as upstream.

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
ends in `crypto.randomUUID()`, and since patch.4 the client id too.

Tests for 2-5: `go/cycletls/index_websocket_test.go` and
`go/cycletls/roundtripper_concurrency_test.go`; each fails on upstream 2.0.5 and passes here
(`go test -race`).

### 8. A dead Go process hung every request until the host restarted (patch.4)

Nothing on the Node side listened for the Go process exiting or its socket closing. Requests sent
after that went into a closed socket, which `ws` drops without an error, and the ones in flight
were never answered, so each waited out its caller's timeout, forever: on 2026-10-31 an API sat in
its maintenance page until it was restarted. Now an exit, a close or a failed send ends every
pending request at once with `WebSocket server not connected: CycleTLS …` (the error ghostfetch
rebuilds its transport on), later requests throw it, and the instance leaves the registry so the
next init spawns a fresh process. The exit is logged with the end of the process's stderr, which is
where an unrecovered panic is written. Requests now listen before they send, so a failed send is
never missed. Upstream's respawn on stderr patterns is gone: it restarted on the same port in a
loop (upstream #428), and it would have killed a healthy process over a recovered panic's log.

### 9. One bad request ended the process and every request in it (patch.4)

A request whose client or URL could not be built hit `log.Fatal` (a proxy URL with an unsupported
scheme, a malformed URL), and a panic in any request's dispatcher ended the process too (a ja3 with
too few fields indexes past its end while dialing). These are now answered with an error frame for
that request alone, with status 0 like a connection closed before any answer (an HTTP status would
read as the target's own answer: ghostfetch would retry nothing and credit the proxy). A request
that fails to parse but carries its id is answered the same way instead of being dropped, which
left its caller waiting out its timeout. A raw connection whose fingerprint could not be applied
is closed instead of leaked.

### 10. The Go server listened on every interface (patch.4)

It bound `:<port>` and accepted any origin, so anyone who could reach the port could send requests
through it, proxies included. It binds `127.0.0.1` now, and the client connects and probes the port
there (`localhost` can resolve to `::1`). It also refuses a handshake that carries an `Origin`
header: the Node client sends none, and a browser always does, so a web page open on the same
machine cannot use the port either.

### 11. brotli v1.2.7 (patch.4)

v1.2.0 panics on a crafted stream (upstream PR #430). utls decodes brotli for TLS certificate
compression on a goroutine no recover covers, so a server could end the process with it.

### 12. A dropped SYN to the proxy held every dial for ~2 minutes (patch.4)

The TCP connect to the proxy had no deadline of its own (upstream #340), and HTTP/2 redials pass a
context without one, so the roundTripper's lock, and every dial behind it, waited out the OS's
connect retries. The dialer now gives up after 15 s.

### 13. A write into a dead path blocked a connection for ~15 minutes (patch.4)

fhttp's HTTP/2 is a 2021 copy of `x/net` without `WriteByteTimeout` (golang/go#48830). It writes
under the connection's locks, so one write the peer never read blocked every request on the
connection, and the health ping of fix 6 with them (golang/go#48810), until TCP gave up. Each write
on a TLS connection handed to a transport now has a 15 s deadline.

### 14. Built with go1.26.10 (patch.4)

patch.3's binaries were built with go1.26.1, before the crypto/x509 fixes utls depends on to verify
certificates (GO-2026-4946, GO-2026-4947, GO-2026-6809). `fork/build.sh` pins the toolchain.

Tests for 8-13: `tests/transport-lost.test.js` (kills the Go process with a request in flight),
`go/cycletls/index_crash_test.go` and `go/cycletls/writedeadline_test.go`; each fails on
2.0.5-patch.3 (the request hangs, the test binary exits on `log.Fatal`, the write never returns)
and passes here.

## Use

```ts
import initCycleTLS from '@emircansahin/cycletls';
```

Same API as `cycletls@2.0.5`. Source: the `fixes/2.0.5` branch of
[emircan-sahin/cycletls](https://github.com/emircan-sahin/cycletls/tree/fixes/2.0.5); to rebuild,
`npm install` (for tsc), then `fork/build.sh <out-dir>` there.
