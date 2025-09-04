package exampleapiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrorDetail represents the ErrorDetail schema
type ErrorDetail struct {
	Location string `json:"location,omitempty" doc:"Where the error occurred, e.g. 'body.items[3].tags' or 'path.thing-id'"`
	Message  string `json:"message,omitempty" doc:"Error message text"`
	Value    any    `json:"value,omitempty" doc:"The value at the given location"`
}

// ErrorModel represents the ErrorModel schema
type ErrorModel struct {
	Detail   string        `json:"detail,omitempty" doc:"A human-readable explanation specific to this occurrence of the problem." example:"Property foo is required but is missing."`
	Errors   []ErrorDetail `json:"errors,omitempty" doc:"Optional list of individual error details"`
	Instance string        `json:"instance,omitempty" doc:"A URI reference that identifies the specific occurrence of the problem." format:"uri" example:"https://example.com/error-log/abc123"`
	Status   int64         `json:"status,omitempty" doc:"HTTP status code" format:"int64" example:"400"`
	Title    string        `json:"title,omitempty" doc:"A short, human-readable summary of the problem type. This value should not change between occurrences of the error." example:"Bad Request"`
	Type     string        `json:"type,omitempty" doc:"A URI reference to human-readable documentation for the error." default:"about:blank" format:"uri" example:"https://example.com/errors/example"`
}

// Thing represents the Thing schema
type Thing struct {
	DeprecatedField string `json:"deprecatedField" doc:"This field is deprecated" deprecated:"true"`
	ID              string `json:"id" doc:"The unique identifier for the thing" minLength:"8" pattern:"^[a-z0-9_-]+$" example:"thing123"`
	Name            string `json:"name" doc:"The name of the thing" minLength:"3" example:"My Thing"`
	ReadOnlyID      string `json:"readOnlyId" doc:"Read-only identifier" readOnly:"true"`
	WriteOnlyToken  string `json:"writeOnlyToken" doc:"Write-only authentication token" writeOnly:"true"`
}

// Option is a functional option for customizing requests
type Option func(*RequestOptions)

// OptionsApplier is an interface for operation-specific options
type OptionsApplier interface {
	Apply(*RequestOptions)
}

// RequestOptions contains optional parameters for API requests
type RequestOptions struct {
	CustomHeaders map[string]string
	CustomQuery   map[string]string
	Body          any
}

// Functional options for customizing requests
func WithHeader(key, value string) Option {
	return func(opts *RequestOptions) {
		if opts.CustomHeaders == nil {
			opts.CustomHeaders = make(map[string]string)
		}
		opts.CustomHeaders[key] = value
	}
}

func WithQuery(key, value string) Option {
	return func(opts *RequestOptions) {
		if opts.CustomQuery == nil {
			opts.CustomQuery = make(map[string]string)
		}
		opts.CustomQuery[key] = value
	}
}

func WithBody(body any) Option {
	return func(opts *RequestOptions) {
		opts.Body = body
	}
}

// WithOptions applies operation-specific optional parameters
func WithOptions(applier OptionsApplier) Option {
	return func(opts *RequestOptions) {
		applier.Apply(opts)
	}
}

// ListThingsOptions contains optional parameters for ListThings
type ListThingsOptions struct {
	Limit  int64  `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// Apply implements OptionsApplier for ListThingsOptions
func (o ListThingsOptions) Apply(opts *RequestOptions) {
	if o.Limit != 0 {
		if opts.CustomQuery == nil {
			opts.CustomQuery = make(map[string]string)
		}
		opts.CustomQuery["limit"] = fmt.Sprintf("%v", o.Limit)
	}
	if o.Cursor != "" {
		if opts.CustomQuery == nil {
			opts.CustomQuery = make(map[string]string)
		}
		opts.CustomQuery["cursor"] = o.Cursor
	}
}

// ExampleAPIClient defines the interface for the API client
type ExampleAPIClient interface {
	ListThings(ctx context.Context, opts ...Option) (*http.Response, []Thing, error)
	GetThingsByID(ctx context.Context, id string, opts ...Option) (*http.Response, Thing, error)
	Follow(ctx context.Context, link string, result any, opts ...Option) (*http.Response, error)
}

// ExampleAPIClientImpl implements the ExampleAPIClient interface
type ExampleAPIClientImpl struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new ExampleAPIClient with default HTTP client
func New(baseURL string) ExampleAPIClient {
	return NewWithClient(baseURL, nil)
}

// NewWithClient creates a new ExampleAPIClient with custom base URL and HTTP client
func NewWithClient(baseURL string, client *http.Client) ExampleAPIClient {
	if client == nil {
		client = &http.Client{}
	}
	return &ExampleAPIClientImpl{
		baseURL:    baseURL,
		httpClient: client,
	}
}

// ListThings calls the GET /things endpoint
func (c *ExampleAPIClientImpl) ListThings(ctx context.Context, opts ...Option) (*http.Response, []Thing, error) {
	// Apply options
	reqOpts := &RequestOptions{}
	for _, opt := range opts {
		opt(reqOpts)
	}

	// Build URL with path parameters
	pathTemplate := "/things"

	u, err := url.Parse(c.baseURL + pathTemplate)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Add query parameters
	q := u.Query()
	// Add custom query parameters
	for key, value := range reqOpts.CustomQuery {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()

	// Prepare request body
	var reqBody io.Reader

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type for request body

	// Add headers
	// Add custom headers
	for key, value := range reqOpts.CustomHeaders {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle error responses
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return resp, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	var result []Thing
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return resp, nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, result, nil
}

// GetThingsByID calls the GET /things/{id} endpoint
func (c *ExampleAPIClientImpl) GetThingsByID(ctx context.Context, id string, opts ...Option) (*http.Response, Thing, error) {
	// Apply options
	reqOpts := &RequestOptions{}
	for _, opt := range opts {
		opt(reqOpts)
	}

	// Build URL with path parameters
	pathTemplate := "/things/{id}"
	pathTemplate = strings.ReplaceAll(pathTemplate, "{id}", url.PathEscape(id))

	u, err := url.Parse(c.baseURL + pathTemplate)
	if err != nil {
		return nil, Thing{}, fmt.Errorf("invalid URL: %w", err)
	}

	// Add query parameters
	q := u.Query()
	// Add custom query parameters
	for key, value := range reqOpts.CustomQuery {
		q.Set(key, value)
	}
	u.RawQuery = q.Encode()

	// Prepare request body
	var reqBody io.Reader

	// Create request
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), reqBody)
	if err != nil {
		return nil, Thing{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type for request body

	// Add headers
	// Add custom headers
	for key, value := range reqOpts.CustomHeaders {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, Thing{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle error responses
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return resp, Thing{}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	var result Thing
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return resp, Thing{}, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, result, nil
}

// Follow follows a link to retrieve a related resource
func (c *ExampleAPIClientImpl) Follow(ctx context.Context, link string, result any, opts ...Option) (*http.Response, error) {
	// Apply options
	reqOpts := &RequestOptions{}
	for _, opt := range opts {
		opt(reqOpts)
	}

	// Parse the link URL
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("invalid link URL: %w", err)
	}

	// Add custom query parameters
	if len(reqOpts.CustomQuery) > 0 {
		q := u.Query()
		for key, value := range reqOpts.CustomQuery {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	// Prepare request body if provided
	var reqBody io.Reader
	if reqOpts.Body != nil {
		jsonData, err := json.Marshal(reqOpts.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}

	// Create request (assume GET unless body is provided)
	method := "GET"
	if reqBody != nil {
		method = "POST"
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type for request body
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Add custom headers
	for key, value := range reqOpts.CustomHeaders {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle error responses
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return resp, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body into provided result type
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return resp, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, nil
}
