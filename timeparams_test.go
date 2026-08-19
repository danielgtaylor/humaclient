package humaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// timeParamAPI has a date-time parameter both as an optional query param and as a
// required one, since the two render through different template paths.
func timeParamAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Time API", "1.0.0"))

	huma.Get(api, "/events", func(ctx context.Context, input *struct {
		Since time.Time `query:"since" doc:"Only events after this time"`
	}) (*struct {
		Body struct {
			Count int `json:"count"`
		}
	}, error) {
		return nil, nil
	})
	return api
}

// TestTimeParamIsFormattedNotStringified covers the value half of the parameter
// rendering ladder for date-times. The zero-check half already had a time.Time arm
// (`!o.Since.IsZero()`), but the value half fell through to fmt.Sprintf, which renders
// Go's own layout — "2024-01-02 03:04:05 +0000 UTC" — and huma rejects it with 422.
// So every generated client for an API with a date-time parameter sent a value its
// own server could not parse.
func TestTimeParamIsFormattedNotStringified(t *testing.T) {
	src := generateInto(t, timeParamAPI(), "timeapiclient")

	if strings.Contains(src, `opts.CustomQuery["since"] = fmt.Sprintf("%v", o.Since)`) {
		t.Error("date-time param still stringified with Sprintf, which huma rejects with 422")
	}
	if !strings.Contains(src, `opts.CustomQuery["since"] = o.Since.Format(time.RFC3339Nano)`) {
		t.Error("date-time param is not formatted as RFC 3339")
	}

	var mu sync.Mutex
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query().Get("since")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"count": 1})
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";"time";c "testprogram/timeapiclient")
func main() {
	cl := c.New(os.Args[1])
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	_, _, err := cl.GetEvents(context.Background(), c.WithOptions(c.GetEventsOptions{Since: when}))
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
}`
	runGeneratedProgram(t, prog, server.URL)

	mu.Lock()
	defer mu.Unlock()
	// The layout huma parses. Anything else is a 422 from a real server.
	if _, err := time.Parse(time.RFC3339Nano, got); err != nil {
		t.Errorf("query value %q is not RFC 3339: %v", got, err)
	}
}

// TestTimeParamAcceptedByHumaServer is the end-to-end proof: the value the generated
// client sends has to be one huma itself will parse, not merely a plausible string.
func TestTimeParamAcceptedByHumaServer(t *testing.T) {
	src := generateInto(t, timeParamAPI(), "timeapiclient")
	if !strings.Contains(src, "Format(time.RFC3339Nano)") {
		t.Fatal("precondition: the generated client does not format date-times")
	}

	// A real huma server, which validates the parameter against its schema.
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Time API", "1.0.0"))
	var mu sync.Mutex
	var seen time.Time
	huma.Get(api, "/events", func(ctx context.Context, input *struct {
		Since time.Time `query:"since"`
	}) (*struct {
		Body struct {
			Count int `json:"count"`
		}
	}, error) {
		mu.Lock()
		seen = input.Since
		mu.Unlock()
		return &struct {
			Body struct {
				Count int `json:"count"`
			}
		}{}, nil
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";"time";c "testprogram/timeapiclient")
func main() {
	cl := c.New(os.Args[1])
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	resp, _, err := cl.GetEvents(context.Background(), c.WithOptions(c.GetEventsOptions{Since: when}))
	fmt.Printf("status=%d err=%v\n", resp.StatusCode, err)
	if resp.StatusCode != 200 { os.Exit(1) }
}`
	out := runGeneratedProgram(t, prog, server.URL)
	if !strings.Contains(out, "status=200 err=<nil>") {
		t.Errorf("huma rejected the generated client's date-time value:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC); !seen.Equal(want) {
		t.Errorf("server parsed %v, want %v", seen, want)
	}
}

// timeListParamAPI has a list-valued date-time parameter, which renders through
// joinParamValues rather than through the scalar date-time arm.
func timeListParamAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Time List API", "1.0.0"))

	huma.Get(api, "/windows", func(ctx context.Context, input *struct {
		At []time.Time `query:"at" doc:"Instants of interest"`
	}) (*struct {
		Body struct {
			Count int `json:"count"`
		}
	}, error) {
		return nil, nil
	})
	return api
}

// TestTimeListParamElementsAreFormatted covers date-times inside a list. Fixing the
// scalar arm alone left this path stringifying each element with %v, so a
// []time.Time still sent Go's layout and still drew a 422 — the same bug one level
// down.
func TestTimeListParamElementsAreFormatted(t *testing.T) {
	src := generateInto(t, timeListParamAPI(), "timelistapiclient")

	if !strings.Contains(src, "case time.Time:") {
		t.Error("joinParamValues does not special-case date-time elements")
	}

	var mu sync.Mutex
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query().Get("at")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"count": 1})
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";"time";c "testprogram/timelistapiclient")
func main() {
	cl := c.New(os.Args[1])
	a := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	b := time.Date(2024, 6, 7, 8, 9, 10, 0, time.UTC)
	_, _, err := cl.GetWindows(context.Background(), c.WithOptions(c.GetWindowsOptions{At: []time.Time{a, b}}))
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
}`
	runGeneratedProgram(t, prog, server.URL)

	mu.Lock()
	defer mu.Unlock()
	for _, part := range strings.Split(got, ",") {
		if _, err := time.Parse(time.RFC3339Nano, part); err != nil {
			t.Errorf("list element %q is not RFC 3339 (full value %q): %v", part, got, err)
		}
	}
}

// TestJoinHelperOmitsTimeCaseWithoutTimeLists guards the import gate: the generated
// client imports "time" only when a type needs it, so referencing time.Time in the
// helper unconditionally would break every client that has a list param but no
// date-time.
func TestJoinHelperOmitsTimeCaseWithoutTimeLists(t *testing.T) {
	src := generateInto(t, listParamAPI(), "listparamapiclient")
	if !strings.Contains(src, "func joinParamValues") {
		t.Fatal("precondition: helper not emitted")
	}
	if strings.Contains(src, "case time.Time:") {
		t.Error("date-time case emitted for an API with no date-time list params")
	}
}
