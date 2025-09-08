package main

import (
	"context"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/danielgtaylor/humaclient"
)

type Thing struct {
	ID              string `json:"id" doc:"The unique identifier for the thing" minLength:"8" pattern:"^[a-z0-9_-]+$"`
	Name            string `json:"name" doc:"The name of the thing" minLength:"3" example:"My Thing"`
	ReadOnlyID      string `json:"readOnlyId" doc:"Read-only identifier" readOnly:"true"`
	WriteOnlyToken  string `json:"writeOnlyToken" doc:"Write-only authentication token" writeOnly:"true"`
	DeprecatedField string `json:"deprecatedField" doc:"This field is deprecated" deprecated:"true"`
}

type GetThingResponse struct {
	Body Thing
}

type ListThingsResponse struct {
	Link string `header:"Link" doc:"Link to the next page of results"`
	Body []Thing
}

func main() {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Example API", "1.0.0"))

	// Add operations
	huma.Get(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*GetThingResponse, error) {
		return &GetThingResponse{
			Body: Thing{
				ID:   input.ID,
				Name: "Example Thing",
			},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-things",
		Method:      http.MethodGet,
		Path:        "/things",
		Summary:     "List things",
		Description: "Returns a paginated list of things",
	}, func(ctx context.Context, input *struct {
		Limit  int    `query:"limit" doc:"Maximum number of items to return" default:"10"`
		Cursor string `query:"cursor" doc:"Pagination cursor"`
	}) (*ListThingsResponse, error) {
		return &ListThingsResponse{
			Body: []Thing{
				{ID: "thing1", Name: "First Thing"},
				{ID: "thing2", Name: "Second Thing"},
			},
		}, nil
	})

	// Register for client generation
	humaclient.Register(api)

	log.Fatal(http.ListenAndServe(":8080", mux))
}
