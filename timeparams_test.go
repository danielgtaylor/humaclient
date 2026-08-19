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
