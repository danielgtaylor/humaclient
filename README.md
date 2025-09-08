# Huma Client

A library to generate simple clients in Go for interacting with [Huma](https://github.com/danielgtaylor/huma)-based API services. Features:

- Generate a client SDK directly from a `huma.API` definition.
- Convenient hook to add one-line client generation to your APIs.
- Maintains Huma docs/validation tags for model re-use in other APIs.
- Built-in support for paginated responses via `Link` headers with `rel=next`.

These are _not_ supported:

- Union types `oneOf`, `anyOf`, and `type` arrays other than nullable types.
- Non-JSON request and response bodies.

## Example

Given a simple Huma API like this:

```go
package main

import (
	"github.com/danielgtaylor/huma/v2"
)

type Thing struct {
	ID   string `json:"id" doc:"The unique identifier for the thing" minLength:"8" pattern="^[a-z0-9_-]+$"`
	Name string `json:"name" doc:"The name of the thing" minLength:"3"`
}

type GetThingResponse struct {
	Body Thing
}

func main() {
	mux := http.NewServeMux()

	api := humago.New(mux, huma.DefaultConfig("Example API", "1.0.0"))

	huma.Get(api, "/things/{thingID}", func(ctx context.Context, input *struct{
		ThingID string `path:"thingID"`
	}) (*GetThingResponse, error) {
		return &GetThingResponse{
			Body: Thing{
				ID:   input.ThingID,
				Name: "Example Thing",
			},
		}, nil
	})

	http.ListenAndServe(":8080", mux)
}
```

You can add support for generating a client SDK by including one line before starting the server (or initializing any dependencies like databases if you use them).

```go
import (
	"github.com/danielgtaylor/humaclient"
)

// ...

humaclient.Register(api)
http.ListenAndServe(":8080", mux)
```

Now you can run your service like `GENERATE_CLIENT=1 go run .` This will generate a directory based on the API name with a Go client SDK for your API.

You can use it like this:

```go
import (
	"fmt"

	"github.com/your-example/api/exampleapiclient"
)

func main() {
	client := exampleapiclient.New("http://localhost:8080")

	resp, thing, err := client.GetThingByID(ctx, "abc123")
	if err != nil {
		panic(err)
	}

	// Do something with the response and the retrieved thing
	fmt.Println(resp.Header)
	fmt.Println(thing)
}
```

## Features

### Custom Name

You can customize the package and interface name for your generated SDK.

```go
import (
	"github.com/danielgtaylor/humaclient"
)

// ...

humaclient.RegisterWithOptions(api, humaclient.Options{
	PackageName: "custompkg",
	ClientName:  "CustomClient",
})
```

### Custom Output Directory

You can customize the output directory where the generated SDK is created.

```go
import (
	"github.com/danielgtaylor/humaclient"
)

// ...

humaclient.RegisterWithOptions(api, humaclient.Options{
	OutputDirectory: "./generated/clients/myapi",
})
```

If not specified, the generated SDK will be placed in a directory named after the package name in the current working directory.

### Custom Client

You can provide a custom HTTP client to handle e.g. authentication using functionality built into the Go standard library:

```go
type HeadersTransport struct {
	Transport http.RoundTripper
	Headers   map[string]string
}

func (t *HeadersTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for key, value := range t.Headers {
		req.Header.Set(key, value)
	}
	return t.Transport.RoundTrip(req)
}

// ...

client := exampleapiclient.NewWithClient("http://localhost:8080", &http.Client{
	Transport: &HeadersTransport{
		Transport: http.DefaultTransport,
		Headers: map[string]string{
			"Authorization": "Bearer " + token,
		},
	},
})
```

The [`golang.org/x/oauth2`](https://pkg.go.dev/golang.org/x/oauth2) package is useful for this as well.

### Request Bodies

Request bodies, when required, become a part of the generated client methods.

```go
client.PutThingByID(ctx, "abc123", Thing{
	ID:   "abc123",
	Name: "Updated Thing",
})
```

Read on for optional request bodies.

### Optional Parameters

You can set optional defined parameters as well as custom query params or headers when making outgoing requests.

```go
// Custom header example
client.GetThingByID(ctx, "abc123", exampleapiclient.WithHeader("X-Custom-Header", "value"))

// Custom query param example
client.GetThingByID(ctx, "abc123", exampleapiclient.WithQuery("include", "related"))

// Passing an optional body
client.OptionalBodyExample(ctx, "abc123", exampleapiclient.WithBody(Thing{
	ID:   "abc123",
	Name: "Updated Thing",
}))
```

If optional parameters are defined by the API spec, then they can be used as functional options when making requests using a struct specific to that operation:

```go
// Passing API-defined optional parameters
client.ListThings(ctx, exampleapiclient.WithOptions(exampleapiclient.ListThingsOptions{
	Cursor: "abc123",
	Limit: 100,
}))
```

### Pagination

Pagination is supported via the standard `Link` header with a relationship like `rel=next`. If that header is documented in your API and the response returns a list of resources, then a method will be generated to provide an iterator that returns each item in the collection, transparently fetching the next request as needed until no pages remain.

```go
// Example of using the pagination iterator
for item, err := range client.ListThingsPaginator(ctx) {
	if err != nil {
		fmt.Println("Error:", err)
		break
	}
	fmt.Println(item)
}
```

### Following Links

Sometimes a response may contain a link to a related resource. There are various mechanisms for accomplishing this, and the client generator is non-opinionated about how you generate and share such links in your response.

Once a link is received by the client, you can follow it to retrieve the related resource and return the appropriate Go type from the response.

```go
// Get a link from a response in some way, e.g. a `Self` field or `Link` header
link := "..."

// Follow the link to fetch the related resource.
var related Thing
resp, err := client.Follow(ctx, link, &related)
if err != nil {
	panic(err)
}
```

You can also pass custom params when following links:

```go
var related Thing
_, err := client.Follow(ctx, link, &related, exampleapiclient.WithQuery("some", "value"))
```

### Model Reuse

Models in the generated SDK code contain the docs and validation in the original API models, meaning they can be re-used in another Huma API. When you have many microservices this may be be desirable for one service to collect information from others and expose it as a single endpoint or proxy.

For example, the generated code for the `Thing` above would look like this:

```go
type Thing struct {
	ID   string `json:"id" doc:"The unique identifier for the thing" minLength:"8" pattern="^[a-z0-9_-]+$"`
	Name string `json:"name" doc:"The name of the thing" minLength:"3"`
}
```

The inclusion of the `doc`, `minLength`, and `pattern` validation fields is preserved so they can be re-used as a request or response object in another Huma API. All of the fields at https://huma.rocks/features/request-validation/ are supported.

### Model Referencing

In some cases the models may come from a shared package, allowing for easier reuse across different services. The generated SDK code supports this by opting-in to the list of allowed packages that can be imported and used in the generated code.

```go
humaclient.RegisterWithOptions(api, humaclient.Options{
	AllowedPackages: []string{"github.com/danielgtaylor/huma/v2"}
})
```

If, for example, an operation returns a `huma.Schema` response body, the generated code will now reference import `github.com/danielgtaylor/huma/v2` and the generated operation will return a `huma.Schema` as well rather than redefining the `Schema` struct in the generated code.

Use this feature with care as you can unintentionally break clients by changing shared library code!

## Development

### CI/CD Pipeline

This project uses GitHub Actions for continuous integration and delivery. The pipeline runs on every push and pull request.

### Running Tests Locally

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -v -race -coverprofile=coverage.out ./...

# Run benchmarks
go test -bench=. -benchmem ./...

# Test client generation
cd example
GENERATE_CLIENT=1 go run main.go
```

### Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes and add tests
4. Ensure all tests pass and linting is clean
5. Commit your changes (`git commit -m 'Add amazing feature'`)
6. Push to the branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

The CI pipeline will automatically run all tests and quality checks on your PR.

## License

This project is licensed under the MIT License. See the [LICENSE.md](LICENSE.md) file for details.
