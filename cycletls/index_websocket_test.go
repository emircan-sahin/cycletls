package cycletls

import (
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsServer serves WSEndpoint and closes the returned channel when the handler returns.
func wsServer(t *testing.T) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	returned := make(chan struct{})
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		defer close(returned)
		WSEndpoint(w, r)
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+strings.TrimPrefix(server.URL, "http://"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, returned
}

func waitReturned(t *testing.T, returned <-chan struct{}, limit time.Duration) {
	t.Helper()
	select {
	case <-returned:
	case <-time.After(limit):
		t.Fatalf("WSEndpoint still running %v after the client left", limit)
	}
}

// A client that disconnected without "exit" left its socket open (CLOSED in lsof) and WSEndpoint
// blocked forever: one fd and three goroutines per client.
func TestWSEndpoint_ReturnsWhenClientDisconnects(t *testing.T) {
	conn, returned := wsServer(t)
	conn.UnderlyingConn().Close()
	waitReturned(t, returned, 2*time.Second)
}

// Same with a request still in flight: its dispatcher's sends must not block once the socket is gone.
func TestWSEndpoint_ReturnsAfterInFlightRequest(t *testing.T) {
	target := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer target.Close()

	conn, returned := wsServer(t)
	request, _ := json.Marshal(map[string]any{
		"requestId": "c#x#1",
		"options":   map[string]any{"url": target.URL + "/", "method": "GET", "timeout": 5, "userAgent": "ua"},
	})
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		t.Fatal(err)
	}
	conn.UnderlyingConn().Close()
	waitReturned(t, returned, 3*time.Second)
}

// A message that is not JSON ended the client's request loop while its socket stayed open.
func TestWSEndpoint_SurvivesInvalidJSON(t *testing.T) {
	conn, returned := wsServer(t)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"exit"}`)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("want the exit close frame after an invalid message, got %v", err)
	}
	waitReturned(t, returned, 2*time.Second)
}
