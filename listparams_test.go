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

	var gotHeader, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("If-Match")
		gotQuery = r.URL.Query().Get("tags")
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

	if gotHeader != `"a","b"` {
		t.Errorf("If-Match header = %q, want %q", gotHeader, `"a","b"`)
	}
	if gotQuery != "x,y" {
		t.Errorf("tags query = %q, want %q", gotQuery, "x,y")
	}
}
