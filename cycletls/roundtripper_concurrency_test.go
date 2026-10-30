package cycletls

import (
	"context"
	"errors"
	"net"
	"testing"

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
