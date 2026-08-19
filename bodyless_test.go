package humaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// bodylessAPI has an operation that declares a JSON response body but can still
// answer 304 — any conditional GET can — plus a DELETE that answers 204 and declares
// no body at all. huma's autopatch produces the same 304 for a patch that changes
// nothing; it is not used here only to avoid adding dependencies for a test.
func bodylessAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Bodyless API", "1.0.0"))

	type thing struct {
		Name string `json:"name,omitempty"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-thing", Method: http.MethodGet, Path: "/thing",
	}, func(ctx context.Context, _ *struct {
		IfNoneMatch string `header:"If-None-Match"`
	}) (*struct{ Body thing }, error) {
		return &struct{ Body thing }{Body: thing{Name: "a"}}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "delete-thing", Method: http.MethodDelete, Path: "/thing",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return nil, nil
	})
	return api
}

// TestBodylessSuccessResponses covers the statuses RFC 9110 defines as carrying no
// content. Previously the generated client decoded a body on any status below 400, so
// a 304 from autopatch surfaced as `failed to decode response: EOF` — turning a
// reconciler's steady state into an error.
func TestBodylessSuccessResponses(t *testing.T) {
	src := generateInto(t, bodylessAPI(), "bodylessapiclient")

	t.Run("EmitsTheGuard", func(t *testing.T) {
		if !strings.Contains(src, "func bodyAllowedForStatus(status int) bool {") {
			t.Fatal("bodyAllowedForStatus helper not emitted")
		}
		if n := strings.Count(src, "if !bodyAllowedForStatus(resp.StatusCode) {"); n < 2 {
			t.Errorf("guard appears %d times, want at least 2 (operation decode and Follow)", n)
		}
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Query().Get("reset") == "1":
			w.WriteHeader(http.StatusResetContent)
		case r.URL.Query().Get("nocontent") == "1":
			// A 204 from an operation that declares a JSON body. This is the case
			// that actually reaches the decode, unlike the DELETE below.
			w.WriteHeader(http.StatusNoContent)
		case r.Header.Get("If-None-Match") != "":
			w.WriteHeader(http.StatusNotModified) // no body, as RFC 9110 requires
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"name": "decoded"})
		}
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";c "testprogram/bodylessapiclient")
func main() {
	cl := c.New(os.Args[1])
	ctx := context.Background()

	resp, _, err := cl.GetThing(ctx, c.WithHeader("If-None-Match", "\"v1\""))
	fmt.Printf("conditional status=%d err=%v\n", resp.StatusCode, err)

	nresp, nbody, err := cl.GetThing(ctx, c.WithQuery("nocontent", "1"))
	fmt.Printf("declared-body-204 status=%d name=%q err=%v\n", nresp.StatusCode, nbody.Name, err)

	rresp, rbody, err := cl.GetThing(ctx, c.WithQuery("reset", "1"))
	fmt.Printf("declared-body-205 status=%d name=%q err=%v\n", rresp.StatusCode, rbody.Name, err)

	_, body, err := cl.GetThing(ctx)
	fmt.Printf("get name=%q err=%v\n", body.Name, err)

	dresp, err := cl.DeleteThing(ctx)
	fmt.Printf("delete status=%d err=%v\n", dresp.StatusCode, err)
}`
	out := runGeneratedProgram(t, prog, server.URL)

	t.Run("NotModifiedIsNotAnError", func(t *testing.T) {
		if !strings.Contains(out, "conditional status=304 err=<nil>") {
			t.Errorf("want a 304 with a nil error, got:\n%s", out)
		}
	})
	t.Run("StatusWithABodyStillDecodes", func(t *testing.T) {
		if !strings.Contains(out, `get name="decoded" err=<nil>`) {
			t.Errorf("200 no longer decodes, got:\n%s", out)
		}
	})
	// The load-bearing 204: the operation declares a JSON body, so without the guard
	// the empty response reaches the decoder and fails.
	t.Run("NoContentOnAnOperationDeclaringABodyIsNotAnError", func(t *testing.T) {
		if !strings.Contains(out, `declared-body-204 status=204 name="" err=<nil>`) {
			t.Errorf("want a 204 with a nil error and a zero-value body, got:\n%s", out)
		}
	})

	// RFC 9110 says a 205 "cannot contain content" too. net/http's own helper omits
	// it, so this is the one place the generated helper deliberately differs.
	t.Run("ResetContentIsNotAnError", func(t *testing.T) {
		if !strings.Contains(out, `declared-body-205 status=205 name="" err=<nil>`) {
			t.Errorf("want a 205 with a nil error and a zero-value body, got:\n%s", out)
		}
	})

	// A DELETE that declares no body never reached the decoder either way, so this
	// only guards against the bodyless path regressing some other way.
	t.Run("OperationDeclaringNoBodyStillSucceeds", func(t *testing.T) {
		if !strings.Contains(out, "delete status=204 err=<nil>") {
			t.Errorf("want a 204 with a nil error, got:\n%s", out)
		}
	})
}

// TestFollowSkipsBodylessResponse covers the second decode site. Follow is the
// hypermedia entry point, so a conditional GET through it hits the same problem, and
// until now only the presence of the guard in the source was asserted.
func TestFollowSkipsBodylessResponse(t *testing.T) {
	generateInto(t, bodylessAPI(), "bodylessapiclient")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": "followed"})
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";c "testprogram/bodylessapiclient")
type thing struct{ Name string ` + "`json:\"name\"`" + ` }
func main() {
	cl := c.New(os.Args[1])
	ctx := context.Background()

	var got thing
	resp, err := cl.Follow(ctx, os.Args[1]+"/thing", &got)
	fmt.Printf("follow status=%d name=%q err=%v\n", resp.StatusCode, got.Name, err)

	var untouched thing
	nresp, err := cl.Follow(ctx, os.Args[1]+"/thing", &untouched, c.WithHeader("If-None-Match", "\"v1\""))
	fmt.Printf("follow-304 status=%d name=%q err=%v\n", nresp.StatusCode, untouched.Name, err)
}`
	out := runGeneratedProgram(t, prog, server.URL)

	if !strings.Contains(out, `follow status=200 name="followed" err=<nil>`) {
		t.Errorf("Follow no longer decodes a normal response:\n%s", out)
	}
	if !strings.Contains(out, `follow-304 status=304 name="" err=<nil>`) {
		t.Errorf("Follow reports a 304 as an error, or wrote into the result:\n%s", out)
	}
}
