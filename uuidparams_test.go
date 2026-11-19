//go:build go1.27

// The generated client imports the standard library uuid package, which Go 1.27
// added, so these tests only build on Go 1.27 or newer.

package humaclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// uuidThing is a body with a UUID in each shape a field can take: required,
// optional, pointer and slice.
type uuidThing struct {
	ID      string   `json:"id" format:"uuid"`
	Owner   string   `json:"owner,omitempty" format:"uuid"`
	Parent  *string  `json:"parent,omitempty" format:"uuid"`
	Members []string `json:"members,omitempty"`
}

// uuidAPI has format: uuid in a body and in every parameter location: path, a
// required query param, an optional query param, a list query param and a header.
func uuidAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("UUID API", "1.0.0"))

	huma.Put(api, "/things/{thingID}", func(ctx context.Context, input *struct {
		ThingID string   `path:"thingID" format:"uuid"`
		Tenant  string   `query:"tenant" required:"true" format:"uuid"`
		Owner   string   `query:"owner" format:"uuid"`
		IDs     []string `query:"ids"`
		UserID  string   `header:"X-User-Id" format:"uuid"`
		Body    uuidThing
	}) (*struct{ Body uuidThing }, error) {
		return nil, nil
	})

	// Huma puts a format tag on the array, not on its items, so the item format of
	// the two list fields is set on the spec directly.
	oapi := api.OpenAPI()
	for _, s := range oapi.Components.Schemas.Map() {
		if p, ok := s.Properties["members"]; ok && p.Items != nil {
			p.Items.Format = "uuid"
		}
	}
	for _, p := range oapi.Paths["/things/{thingID}"].Put.Parameters {
		if p.Name == "ids" && p.Schema != nil && p.Schema.Items != nil {
			p.Schema.Items.Format = "uuid"
		}
	}
	return api
}

func TestUUIDFormatStaysStringByDefault(t *testing.T) {
	src := generateInto(t, uuidAPI(), "uuidapiclient")

	if strings.Contains(src, "\t\"uuid\"\n") || strings.Contains(src, "uuid.UUID") {
		t.Error("format: uuid generated uuid.UUID without Options.StdlibUUID, which breaks existing clients")
	}
	for _, want := range []string{
		"ID      string",
		`Owner   string   ` + "`" + `json:"owner,omitempty" format:"uuid"` + "`",
		"Parent  *string",
		"Members []string",
		"tenant string",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("default output lost %q", want)
		}
	}
}

func TestStdlibUUIDOptionGeneratesUUIDTypes(t *testing.T) {
	src := generateIntoWithOptions(t, uuidAPI(), Options{StdlibUUID: true}, "uuidapiclient")

	for _, want := range []string{
		"\t\"uuid\"\n",
		// Required field.
		"ID      uuid.UUID",
		// Optional non-pointer field: omitempty never omits an array, so omitzero.
		`Owner   uuid.UUID   ` + "`" + `json:"owner,omitzero" format:"uuid"` + "`",
		"Parent  *uuid.UUID",
		"Members []uuid.UUID",
		// The path param stays a string, as it does for every format.
		"thingID string, tenant uuid.UUID",
		`requiredQueryValues.Set("tenant", tenant.String())`,
		`if o.Owner != (uuid.UUID{}) {`,
		`opts.CustomQuery["owner"] = o.Owner.String()`,
		`opts.CustomQuery["ids"] = joinParamValues(o.Ids)`,
		"case uuid.UUID:\n\t\t\tparts[i] = e.String()",
		`opts.CustomHeaders["X-User-Id"] = o.XUserID.String()`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated client lacks %q", want)
		}
	}
	if strings.Contains(src, `"owner,omitempty"`) {
		t.Error("an optional uuid.UUID field kept omitempty, which sends the nil UUID instead of leaving it out")
	}
}

// TestStdlibUUIDValuesGoOverTheWire runs the generated client against a server and
// checks every location carries the canonical UUID text, an unset optional UUID is
// left out of the body, and the response decodes into uuid.UUID fields.
func TestStdlibUUIDValuesGoOverTheWire(t *testing.T) {
	generateIntoWithOptions(t, uuidAPI(), Options{StdlibUUID: true}, "uuidapiclient")

	var mu sync.Mutex
	var gotPath, gotTenant, gotOwner, gotIDs, gotUser string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath = r.URL.Path
		gotTenant = r.URL.Query().Get("tenant")
		gotOwner = r.URL.Query().Get("owner")
		gotIDs = r.URL.Query().Get("ids")
		gotUser = r.Header.Get("X-User-Id")
		_ = json.Unmarshal(raw, &gotBody)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2b","owner":"5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2c",` +
			`"members":["5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2d"]}`))
	}))
	t.Cleanup(server.Close)

	prog := `package main
import ("context";"fmt";"os";"uuid";c "testprogram/uuidapiclient")
func main() {
	cl := c.New(os.Args[1])
	parent := uuid.MustParse("00000000-0000-4000-8000-000000000004")
	body := c.UUIDThing{
		ID:      uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		Parent:  &parent,
		Members: []uuid.UUID{uuid.MustParse("00000000-0000-4000-8000-000000000005")},
	}
	opts := c.PutThingsByThingIDOptions{
		Owner:   uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		Ids:     []uuid.UUID{uuid.MustParse("00000000-0000-4000-8000-000000000006"), uuid.MustParse("00000000-0000-4000-8000-000000000007")},
		XUserID: uuid.MustParse("00000000-0000-4000-8000-000000000003"),
	}
	_, got, err := cl.PutThingsByThingID(context.Background(), "00000000-0000-4000-8000-000000000009",
		uuid.MustParse("00000000-0000-4000-8000-00000000000a"), body, c.WithOptions(opts))
	if err != nil { fmt.Println("ERR", err); os.Exit(1) }
	fmt.Println(got.ID, got.Owner, got.Parent == nil, got.Members)
}`
	out := runGeneratedProgramGo(t, "1.27", prog, server.URL)

	if want := "5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2b 5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2c true [5f0c1a2b-3c4d-4e5f-8a6b-7c8d9e0f1a2d]"; strings.TrimSpace(out) != want {
		t.Errorf("decoded response %q, want %q", strings.TrimSpace(out), want)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, c := range []struct{ name, got, want string }{
		{"path", gotPath, "/things/00000000-0000-4000-8000-000000000009"},
		{"required query", gotTenant, "00000000-0000-4000-8000-00000000000a"},
		{"optional query", gotOwner, "00000000-0000-4000-8000-000000000002"},
		{"list query", gotIDs, "00000000-0000-4000-8000-000000000006,00000000-0000-4000-8000-000000000007"},
		{"header", gotUser, "00000000-0000-4000-8000-000000000003"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if gotBody["id"] != "00000000-0000-4000-8000-000000000001" || gotBody["parent"] != "00000000-0000-4000-8000-000000000004" {
		t.Errorf("body ids %v", gotBody)
	}
	if members, _ := gotBody["members"].([]any); len(members) != 1 || members[0] != "00000000-0000-4000-8000-000000000005" {
		t.Errorf("body members %v", gotBody["members"])
	}
	if _, sent := gotBody["owner"]; sent {
		t.Errorf("an unset optional uuid.UUID went out as %v; it must be left out", gotBody["owner"])
	}
}

// An API whose only UUID is a path parameter: path parameters stay string, so the
// generated client must not import uuid, or it does not compile (unused import).
func TestStdlibUUIDPathOnlyParamImportsNoUUID(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("UUID Path API", "1.0.0"))
	huma.Get(api, "/things/{thingID}", func(ctx context.Context, input *struct {
		ThingID string `path:"thingID" format:"uuid"`
	}) (*struct{ Body string }, error) {
		return nil, nil
	})

	src := generateIntoWithOptions(t, api, Options{StdlibUUID: true}, "uuidpathapiclient")
	if strings.Contains(src, `"uuid"`) {
		t.Errorf("a path-only UUID parameter imports uuid:\n%s", src)
	}
	if !strings.Contains(src, "thingID string") {
		t.Errorf("the path parameter is not a string:\n%s", methodBody(t, src, "func (c *"))
	}
}

// A bare uuid.UUID response needs a uuid.UUID{} zero value in the method body, and
// a list header goes through the joinParamValues uuid case; both must compile.
func TestStdlibUUIDBareResponseAndListHeader(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("UUID Response API", "1.0.0"))
	huma.Get(api, "/ids", func(ctx context.Context, input *struct {
		IDs []string `header:"X-Ids"`
	}) (*struct {
		Body string `format:"uuid"`
	}, error) {
		return nil, nil
	})
	for _, p := range api.OpenAPI().Paths["/ids"].Get.Parameters {
		if p.Name == "X-Ids" && p.Schema != nil && p.Schema.Items != nil {
			p.Schema.Items.Format = "uuid"
		}
	}

	src := generateIntoWithOptions(t, api, Options{StdlibUUID: true}, "uuidresponseapiclient")
	for _, want := range []string{
		"(*http.Response, uuid.UUID, error)",
		`opts.CustomHeaders["X-Ids"] = joinParamValues(o.XIds)`,
		"case uuid.UUID:",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated client lacks %q", want)
		}
	}
}

// A path parameter named uuid must not shadow the uuid package the method body
// needs for a bare uuid.UUID response.
func TestStdlibUUIDParamNamedUUID(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("UUID Name API", "1.0.0"))
	huma.Get(api, "/things/{uuid}", func(ctx context.Context, input *struct {
		UUID string `path:"uuid" format:"uuid"`
	}) (*struct {
		Body string `format:"uuid"`
	}, error) {
		return nil, nil
	})

	src := generateIntoWithOptions(t, api, Options{StdlibUUID: true}, "uuidnameapiclient")
	if !strings.Contains(src, "uuidParam string, opts ...Option) (*http.Response, uuid.UUID, error)") {
		t.Errorf("the uuid path parameter was not renamed:\n%s", methodBody(t, src, "func (c *"))
	}
}
