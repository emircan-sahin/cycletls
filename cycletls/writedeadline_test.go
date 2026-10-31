package cycletls

import (
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// A write into a path nobody reads used to block until TCP gave up, holding the HTTP/2
// connection's locks the whole time; it must end at connWriteTimeout.
func TestWriteDeadlineConn_EndsAWriteNobodyReads(t *testing.T) {
	previous := connWriteTimeout
	connWriteTimeout = 100 * time.Millisecond
	defer func() { connWriteTimeout = previous }()

	local, remote := net.Pipe()
	defer remote.Close()
	conn := writeDeadlineConn{utls.UClient(local, &utls.Config{ServerName: "peer.test"}, utls.HelloChrome_Auto)}
	defer conn.Close()

	started := time.Now()
	if _, err := conn.Write([]byte("request")); err == nil {
		t.Fatal("want the unread write to fail")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("want the write ended near %v, took %v", connWriteTimeout, elapsed)
	}
}
