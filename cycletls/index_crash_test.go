package cycletls

import (
	"encoding/binary"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type frame struct {
	requestID, method, message string
	status                     int
}

// readFrame reads the next frame for requestID: id and method length-prefixed, then for "error"
// a status and a length-prefixed message.
func readFrame(t *testing.T, conn *websocket.Conn, requestID string) frame {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no frame for %s: %v", requestID, err)
		}
		read := func() string {
			size := int(binary.BigEndian.Uint16(data))
			value := string(data[2 : 2+size])
			data = data[2+size:]
			return value
		}
		got := frame{requestID: read(), method: read()}
		if got.requestID != requestID {
			continue
		}
		if got.method == "error" {
			got.status = int(binary.BigEndian.Uint16(data))
			data = data[2:]
			got.message = read()
		}
		return got
	}
}

func send(t *testing.T, conn *websocket.Conn, message any) {
	t.Helper()
	raw, _ := json.Marshal(message)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatal(err)
	}
}

// A request the process could not serve used to end it (log.Fatal, or a panic in a dispatcher)
// along with every other request in flight; each must be answered with an error instead, and
// the next request served as usual.
func TestWSEndpoint_AnswersRequestsItCannotServe(t *testing.T) {
	// TLS, so the fingerprint is used.
	target := httptest.NewTLSServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	conn, _ := wsServer(t)
	request := func(id string, options map[string]any) map[string]any {
		base := map[string]any{"url": target.URL + "/", "method": "GET", "timeout": 5, "userAgent": "ua", "insecureSkipVerify": true}
		for key, value := range options {
			base[key] = value
		}
		return map[string]any{"requestId": id, "options": base}
	}

	cases := []struct {
		name    string
		message any
		status  int
	}{
		// newConnectDialer refuses the scheme: was log.Fatal.
		{"invalid proxy", request("c#proxy#1", map[string]any{"proxy": "ftp://user:pass@proxy.test:1"}), noResponseStatus},
		// http.NewRequestWithContext refuses the URL: was log.Fatal.
		{"invalid url", request("c#url#1", map[string]any{"url": "http://%zz"}), noResponseStatus},
		// Headers must be strings: the message was dropped and its caller waited out its timeout.
		{"unparseable", map[string]any{"requestId": "c#parse#1", "options": map[string]any{"url": target.URL, "headers": map[string]any{"a": 1}}}, noResponseStatus},
		// A ja3 with too few fields indexes past its end while dialing: was a process-wide panic.
		{"panicking fingerprint", request("c#ja3#1", map[string]any{"ja3": "771"}), noResponseStatus},
	}
	for _, tc := range cases {
		send(t, conn, tc.message)
		id := tc.message.(map[string]any)["requestId"].(string)
		got := readFrame(t, conn, id)
		if got.method != "error" || got.status != tc.status {
			t.Fatalf("%s: want an error frame with status %d, got %+v", tc.name, tc.status, got)
		}
	}

	send(t, conn, request("c#ok#1", nil))
	if got := readFrame(t, conn, "c#ok#1"); got.method != "response" {
		t.Fatalf("want the next request served, got %+v", got)
	}
}

func TestErrorFrame_CutsAMessageTooLongForItsLengthPrefix(t *testing.T) {
	long := make([]byte, 70_000)
	for i := range long {
		long[i] = 'x'
	}
	data := errorFrame("id", 500, string(long))
	size := int(binary.BigEndian.Uint16(data[len(data)-0xFFFF-2:]))
	if size != 0xFFFF || len(data) != 2+2+2+5+2+2+0xFFFF {
		t.Fatalf("want the message cut to 65535 bytes, got prefix %d and frame %d", size, len(data))
	}
}

// A browser page on the same machine could open the loopback port and send requests through it.
func TestWSEndpoint_RefusesBrowserOrigins(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(WSEndpoint))
	defer server.Close()
	url := "ws://" + strings.TrimPrefix(server.URL, "http://")

	_, response, err := websocket.DefaultDialer.Dial(url, stdhttp.Header{"Origin": {"https://page.test"}})
	if err == nil || response == nil || response.StatusCode != stdhttp.StatusForbidden {
		t.Fatalf("want a browser origin refused with 403, got %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("want the Node client, which sends no Origin, accepted: %v", err)
	}
	conn.Close()
}
