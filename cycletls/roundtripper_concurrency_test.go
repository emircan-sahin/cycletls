package cycletls

import (
	"context"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
)

// Test that getTransport no longer panics when dialTLS returns (conn, nil)
// due to a cached connection/transport already being present for the address.
func TestGetTransport_NoPanicWhenCachedConnPresent(t *testing.T) {
	// Create a new roundTripper
	rtIface := newRoundTripper(Browser{})

	// Type assert to concrete type to access internals
	rt, ok := rtIface.(*roundTripper)
	if !ok {
		t.Fatalf("expected *roundTripper, got %T", rtIface)
	}

	// Build a simple HTTPS request
	req, err := http.NewRequest("GET", "https://example.com/", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Determine the addr key used by roundTripper
	addr := rt.getDialTLSAddr(req)

	// Simulate a previously established (cached) TLS connection and transport
	c1, _ := net.Pipe()
	defer c1.Close()
	rt.cachedConnections[addr] = c1
	rt.cachedTransports[addr] = &http.Transport{}

	// Ensure no panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("did not expect panic: %v", r)
		}
	}()

	if err := rt.getTransport(req, addr); err != nil {
		t.Fatalf("getTransport returned error: %v", err)
	}
}

// A dialer that records each dial and refuses it, so the test never touches the network.
type refusingDialer struct{ dials int }

func (d *refusingDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	d.dials++
	return nil, errors.New("refused")
}

// The connection negotiated in getTransport goes to the transport once. Kept in the cache, a closed
// one came back to every later dial and the host failed for good with "use of closed network connection".
func TestDialTLS_HandsCachedConnOutOnce(t *testing.T) {
	dialer := &refusingDialer{}
	rt := newRoundTripper(Browser{}, dialer).(*roundTripper)
	addr := "example.com:443"

	c1, c2 := net.Pipe()
	defer c2.Close()
	rt.cachedConnections[addr] = c1
	rt.cachedTransports[addr] = &http.Transport{}

	first, err := rt.dialTLS(context.Background(), "tcp", addr)
	if err != nil || first != c1 {
		t.Fatalf("first dial: want the cached conn, got %v, %v", first, err)
	}

	// The transport's conn closed: the next dial must make a new one, not return c1 again.
	c1.Close()
	second, err := rt.dialTLS(context.Background(), "tcp", addr)
	if second == c1 {
		t.Fatal("second dial returned the closed cached conn")
	}
	if err == nil || dialer.dials != 1 {
		t.Fatalf("second dial: want a new dial (refused), got conn %v, err %v, dials %d", second, err, dialer.dials)
	}
}

type stubTransport struct{}

func (stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
}

// RoundTrip read cachedTransports without a lock while dials wrote it: Go aborts the whole process
// on a concurrent map read and write. Run with -race.
func TestRoundTrip_CachedTransportsConcurrentAccess(t *testing.T) {
	rt := newRoundTripper(Browser{}).(*roundTripper)
	rt.cachedTransports["example.com:443"] = stubTransport{}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			req, _ := http.NewRequest("GET", "https://example.com/", nil)
			if _, err := rt.RoundTrip(req); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://host%d.test/", i), nil)
			if err := rt.getTransport(req, rt.getDialTLSAddr(req)); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

// A retry on a redial (the transport exists) replaced the working transport and failed the live
// request with errProtocolNegotiated. It must hand the conn back and keep the transport.
func TestRetries_KeepExistingTransport(t *testing.T) {
	server := httptest.NewTLSServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {}))
	defer server.Close()
	addr := server.Listener.Addr().String()

	retries := map[string]func(*roundTripper) (net.Conn, error){
		"TLS13CompatibleCurves": func(rt *roundTripper) (net.Conn, error) {
			return rt.retryWithTLS13CompatibleCurves(context.Background(), "tcp", addr, "127.0.0.1")
		},
		"OriginalTLS12JA3": func(rt *roundTripper) (net.Conn, error) {
			return rt.retryWithOriginalTLS12JA3(context.Background(), "tcp", addr, "127.0.0.1")
		},
	}
	for name, retry := range retries {
		t.Run(name, func(t *testing.T) {
			rt := newRoundTripper(Browser{JA3: DefaultChrome_JA3, InsecureSkipVerify: true}).(*roundTripper)
			existing := &http.Transport{}
			rt.cachedTransports[addr] = existing

			conn, err := retry(rt)
			if err != nil || conn == nil {
				t.Fatalf("want the conn, got %v, %v", conn, err)
			}
			defer conn.Close()
			if rt.cachedTransports[addr] != existing {
				t.Fatal("the existing transport was replaced")
			}
		})
	}
}

// silentListener accepts connections and never writes, like a stalled proxy or server.
func silentListener(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range accepted {
			conn.Close()
		}
	})
	return listener.Addr().String()
}

// dialWithin fails the test if dialTLS hangs instead of failing within limit.
func dialWithin(t *testing.T, rt *roundTripper, addr string, limit time.Duration) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := rt.dialTLS(context.Background(), "tcp", addr)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("dial to a silent peer succeeded")
		}
	case <-time.After(limit):
		t.Fatalf("dial still blocked after %v", limit)
	}
}

// dialTLS holds rt's lock across the handshake; the HTTP/2 transport redials with
// context.Background(), so a silent server blocked every request on the client.
func TestDialTLS_SilentServerTimesOut(t *testing.T) {
	addr := silentListener(t)
	client, err := createNewClient(Browser{}, 1, false, "ua")
	if err != nil {
		t.Fatal(err)
	}
	dialWithin(t, client.Transport.(*roundTripper), addr, 3*time.Second)
}

// Same through an HTTP proxy that accepts the TCP connection and never answers the CONNECT.
func TestDialTLS_SilentProxyTimesOut(t *testing.T) {
	proxyAddr := silentListener(t)
	client, err := createNewClient(Browser{}, 1, false, "ua", "http://"+proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	dialWithin(t, client.Transport.(*roundTripper), "example.com:443", 3*time.Second)
}
