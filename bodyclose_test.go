package humaclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
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

// rawBodyAPI declares a success response whose media type this generator does not
// decode, so the operation looks bodyless while the response still carries content
// only the caller can interpret.
func rawBodyAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Raw API", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID: "download-file",
		Method:      http.MethodGet,
		Path:        "/file",
		Responses: map[string]*huma.Response{
			"200": {Description: "OK", Content: map[string]*huma.MediaType{
				"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
			}},
		},
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) { return nil, nil })

	// A genuinely bodyless operation alongside it, to confirm the two are told apart
	// rather than the exemption being applied to everything.
	huma.Register(api, huma.Operation{
		OperationID:   "delete-file",
		Method:        http.MethodDelete,
		Path:          "/file",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) { return nil, nil })

	return api
}

// TestRawResponseBodyStaysOpen covers the other operations whose body the caller must
// read for itself. Only application/json is decoded, so a download endpoint looks
// bodyless to the generator; closing its body would leave read-on-closed-body as the
// only possible outcome of calling it.
func TestRawResponseBodyStaysOpen(t *testing.T) {
	src := generateInto(t, rawBodyAPI(), "rawapiclient")

	download := methodBody(t, src, "func (c *RawAPIClientImpl) DownloadFile(")
	if strings.Contains(download, "\n\tdefer resp.Body.Close()") {
		t.Errorf("a non-JSON response body is closed before the caller can read it:\n%s", download)
	}

	// The contract has to be stated where a caller will see it, since the generated
	// method deliberately hands back an open body.
	if !strings.Contains(src, "The response body is neither read nor closed here") {
		t.Error("the generated method does not tell the caller it owns the response body")
	}

	// The genuinely bodyless DELETE in the same API must still be closed, or the
	// exemption is just reintroducing the leak everywhere.
	del := methodBody(t, src, "func (c *RawAPIClientImpl) DeleteFile(")
	if !strings.Contains(del, "\n\tdefer resp.Body.Close()") {
		t.Errorf("a genuinely bodyless operation does not close its body:\n%s", del)
	}
}

// TestRawResponseBodyReadableEndToEnd is the behavioural counterpart: the bytes must
// actually still be there to read once the method has returned.
func TestRawResponseBodyReadableEndToEnd(t *testing.T) {
	src := generateInto(t, rawBodyAPI(), "rawapiclient")
	if strings.Contains(methodBody(t, src, "func (c *RawAPIClientImpl) DownloadFile("), "\n\tdefer resp.Body.Close()") {
		t.Fatal("precondition: the generated client closes the raw body")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("file-contents"))
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"io";"os";c "testprogram/rawapiclient")
func main() {
	cl := c.New(os.Args[1])
	resp, err := cl.DownloadFile(context.Background())
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	fmt.Printf("read=%q err=%v\n", string(b), err)
}`
	if out := runGeneratedProgram(t, prog, server.URL); !strings.Contains(out, `read="file-contents" err=<nil>`) {
		t.Errorf("could not read the raw response body after the call returned:\n%s", out)
	}
}
