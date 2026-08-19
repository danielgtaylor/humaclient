package humaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/danielgtaylor/huma/v2/conditional"
)

// listParamAPI has both a list-valued header and a list-valued query parameter.
// huma's conditional.Params declares If-Match and If-None-Match as lists, which is
// how this shape arises in practice.
func listParamAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("List Param API", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID: "get-thing",
		Method:      http.MethodGet,
		Path:        "/thing",
	}, func(ctx context.Context, input *struct {
		conditional.Params
		Tags []string `query:"tags" doc:"Tags to filter by"`
	}) (*struct {
		Body struct {
			Name string `json:"name"`
		}
	}, error) {
		return nil, nil
	})
	return api
}

// TestListValuedOptionalParams covers the generator emitting a valid zero-check and a
// correct rendered value for list-valued optional parameters. Previously any type not
// matched by an earlier arm fell through to `if o.X != 0`, so a []string parameter
// produced Go that did not compile.
func TestListValuedOptionalParams(t *testing.T) {
	src := generateInto(t, listParamAPI(), "listparamapiclient")

	t.Run("GuardsOnLengthNotZeroComparison", func(t *testing.T) {
		for _, bad := range []string{"if o.IfMatch != 0", "if o.IfNoneMatch != 0", "if o.Tags != 0"} {
			if strings.Contains(src, bad) {
				t.Errorf("list-valued parameter still compared against 0: %q", bad)
			}
		}
		for _, want := range []string{"if len(o.IfMatch) != 0 {", "if len(o.Tags) != 0 {"} {
			if !strings.Contains(src, want) {
				t.Errorf("missing length guard %q", want)
			}
		}
	})

	t.Run("JoinsValuesRatherThanFormattingTheSlice", func(t *testing.T) {
		// fmt.Sprintf("%v", []string{"a","b"}) renders "[a b]", which is not a valid
		// header or query value. The values must be joined instead.
		if strings.Contains(src, `fmt.Sprintf("%v", o.IfMatch)`) || strings.Contains(src, `fmt.Sprintf("%v", o.Tags)`) {
			t.Error("list-valued parameter rendered with fmt.Sprintf, which would emit Go-syntax brackets")
		}
		for _, want := range []string{"joinParamValues(o.IfMatch)", "joinParamValues(o.Tags)"} {
			if !strings.Contains(src, want) {
				t.Errorf("missing %q", want)
			}
		}
	})

	t.Run("HelperOmittedWhenNoListParams", func(t *testing.T) {
		plain := generateInto(t, createTestAPI(), "testapiclient")
		if strings.Contains(plain, "func joinParamValues") {
			t.Error("joinParamValues emitted for an API with no list-valued parameters")
		}
	})
}

// TestListValuedParamsSentOnTheWire checks the joined value actually reaches the
// server as a single comma-separated header, which is the `simple` style OpenAPI
// applies to header parameters by default.
func TestListValuedParamsSentOnTheWire(t *testing.T) {
	src := generateInto(t, listParamAPI(), "listparamapiclient")
	if !strings.Contains(src, "joinParamValues") {
		t.Fatal("precondition: generated client does not join list params")
	}

	// The handler runs on the server's goroutine; guard the captured values rather
	// than relying on the request/response round trip to order them.
	var mu sync.Mutex
	var gotHeader, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHeader = r.Header.Get("If-Match")
		gotQuery = r.URL.Query().Get("tags")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": "ok"})
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";c "testprogram/listparamapiclient")
func main() {
	cl := c.New(os.Args[1])
	_, _, err := cl.GetThing(context.Background(), c.WithOptions(c.GetThingOptions{
		IfMatch: []string{"\"a\"", "\"b\""},
		Tags:    []string{"x", "y"},
	}))
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
}`
	runGeneratedProgram(t, prog, server.URL)

	mu.Lock()
	defer mu.Unlock()
	if gotHeader != `"a","b"` {
		t.Errorf("If-Match header = %q, want %q", gotHeader, `"a","b"`)
	}
	if gotQuery != "x,y" {
		t.Errorf("tags query = %q, want %q", gotQuery, "x,y")
	}
}

// requiredListParamAPI has a list-valued query parameter that is required, so it is
// passed as a positional argument and rendered by the required-query path rather than
// the options struct.
func requiredListParamAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Required List API", "1.0.0"))

	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodGet,
		Path:        "/search",
	}, func(ctx context.Context, input *struct {
		Tags []string `query:"tags" required:"true" doc:"Tags to search for"`
	}) (*struct {
		Body struct {
			Count int `json:"count"`
		}
	}, error) {
		return nil, nil
	})
	return api
}

// TestRequiredListValuedQueryParam covers the required-parameter path, which renders
// separately from the options struct. It is the more dangerous half of the same bug:
// the optional version failed to compile, so it could never ship, whereas this one
// compiled and silently sent Go's slice syntax as the query value.
func TestRequiredListValuedQueryParam(t *testing.T) {
	src := generateInto(t, requiredListParamAPI(), "requiredlistapiclient")

	if strings.Contains(src, `requiredQueryValues.Set("tags", fmt.Sprintf("%v", tags))`) {
		t.Error("required list-valued query param still formatted with Sprintf, which renders Go slice syntax")
	}
	if !strings.Contains(src, `requiredQueryValues.Set("tags", joinParamValues(tags))`) {
		t.Error("required list-valued query param is not joined")
	}
	// The helper is gated on a scan of the parameter collections; required params live
	// in their own, so an API whose only list param is required must still get it.
	if !strings.Contains(src, "func joinParamValues") {
		t.Error("joinParamValues not emitted for an API whose only list param is required")
	}

	var mu sync.Mutex
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query().Get("tags")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"count": 1})
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";c "testprogram/requiredlistapiclient")
func main() {
	cl := c.New(os.Args[1])
	_, _, err := cl.Search(context.Background(), []string{"x", "y"})
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
}`
	runGeneratedProgram(t, prog, server.URL)

	mu.Lock()
	defer mu.Unlock()
	if got != "x,y" {
		t.Errorf("tags query = %q, want %q", got, "x,y")
	}
}
