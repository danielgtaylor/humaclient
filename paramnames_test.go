package humaclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type paramNamesBody struct {
	Name string `json:"name"`
}

// paramNamesAPI names its parameters after method locals (req, u, body, item), an
// imported package (time, with a time.Time response that needs the package in the
// method body), a keyword (type), a predeclared identifier (string), and repeats
// one name across path and query (id).
func paramNamesAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Param Names API", "1.0.0"))

	huma.Register(api, huma.Operation{OperationID: "put-thing", Method: http.MethodPut, Path: "/things/{type}/{req}/{id}"},
		func(ctx context.Context, input *struct {
			Type    string    `path:"type"`
			Req     string    `path:"req"`
			PathID  string    `path:"id"`
			U       string    `query:"u" required:"true"`
			BodyQ   string    `query:"body" required:"true"`
			Item    string    `query:"item" required:"true"`
			String  int       `query:"string" required:"true"`
			Time    time.Time `query:"time" required:"true"`
			QueryID string    `query:"id" required:"true"`
			Body    paramNamesBody
		}) (*struct {
			Body time.Time
		}, error) {
			return nil, nil
		})
	return api
}

func TestParamNamesDoNotShadowGeneratedIdentifiers(t *testing.T) {
	src := generateInto(t, paramNamesAPI(), "paramnamesapiclient")

	want := "PutThing(ctx context.Context, typeParam string, reqParam string, id string, uParam string, " +
		"bodyParam string, itemParam string, stringParam int64, timeParam time.Time, idParam string, body ParamNamesBody, opts ...Option)"
	if !strings.Contains(src, want) {
		t.Errorf("signature lacks %q:\n%s", want, methodBody(t, src, "func (c *"))
	}
}

// TestRenamedParamsKeepTheirWireNames checks each renamed parameter still goes out
// under its own name, in its own place.
func TestRenamedParamsKeepTheirWireNames(t *testing.T) {
	generateInto(t, paramNamesAPI(), "paramnamesapiclient")

	var mu sync.Mutex
	var gotPath, gotQuery, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath, gotQuery, gotBody = r.URL.Path, r.URL.Query().Encode(), string(raw)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`"2026-01-02T03:04:05Z"`))
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";"time";c "testprogram/paramnamesapiclient")
func main() {
	_, got, err := c.New(os.Args[1]).PutThing(context.Background(), "t", "r", "p", "uu", "b", "i", 7,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "q", c.ParamNamesBody{Name: "n"})
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
	fmt.Println(got.Format(time.RFC3339))
}`
	out := strings.TrimSpace(runGeneratedProgram(t, prog, server.URL))
	if out != "2026-01-02T03:04:05Z" {
		t.Errorf("decoded response %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/things/t/r/p" {
		t.Errorf("path %q", gotPath)
	}
	if want := "body=b&id=q&item=i&string=7&time=2026-01-01T00%3A00%3A00Z&u=uu"; gotQuery != want {
		t.Errorf("query %q, want %q", gotQuery, want)
	}
	if strings.TrimSpace(gotBody) != `{"name":"n"}` {
		t.Errorf("body %q", gotBody)
	}
}
