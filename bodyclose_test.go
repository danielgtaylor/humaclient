package humaclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBodylessOperationClosesBody covers the response body leak on the success path
// of an operation with no response body, where the close previously happened only
// inside the >= 400 branch.
func TestBodylessOperationClosesBody(t *testing.T) {
	src := generateInto(t, bodylessAPI(), "bodylessapiclient")

	// DeleteThing has no response body. Its close must be unconditional, not nested
	// in the error branch.
	del := methodBody(t, src, "func (c *BodylessAPIClientImpl) DeleteThing(")
	if !strings.Contains(del, "\n\tdefer resp.Body.Close()") {
		t.Errorf("DeleteThing does not close the response body on the success path:\n%s", del)
	}
	if strings.Count(del, "resp.Body.Close()") != 1 {
		t.Errorf("DeleteThing closes the body %d times, want exactly 1:\n%s", strings.Count(del, "resp.Body.Close()"), del)
	}
}

// TestSSEBodyStaysOpen is the regression guard on the obvious way to fix the leak
// above. An SSE operation's caller reads resp.Body after the method returns — the
// generated ...Stream method does exactly that — so closing it there truncates the
// stream to zero events.
func TestSSEBodyStaysOpen(t *testing.T) {
	src := generateInto(t, createSSETestAPI(), "ssetestapiclient")

	stream := methodBody(t, src, "func (c *SSETestAPIClientImpl) WatchEvents(")
	if strings.Contains(stream, "\n\tdefer resp.Body.Close()") {
		t.Fatalf("SSE operation closes its body before the caller reads it:\n%s", stream)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := range 3 {
			fmt.Fprintf(w, "event: message\ndata: {\"seq\": %d}\n\n", i)
			w.(http.Flusher).Flush()
		}
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";c "testprogram/ssetestapiclient")
func main() {
	cl := c.New(os.Args[1])
	n := 0
	for ev, err := range cl.WatchEventsStream(context.Background()) {
		if err != nil { fmt.Println("ERR", err); os.Exit(1) }
		_ = ev
		n++
	}
	fmt.Printf("events=%d\n", n)
}`
	if out := runGeneratedProgram(t, prog, server.URL); !strings.Contains(out, "events=3") {
		t.Errorf("want 3 SSE events, got:\n%s", out)
	}
}
