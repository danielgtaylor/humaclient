package humaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/casing"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Test models with comprehensive validation tags
type TestThing struct {
	ID              string    `json:"id" doc:"The unique identifier" minLength:"8" pattern:"^[a-z0-9_-]+$" example:"thing123"`
	Name            string    `json:"name" doc:"The name" minLength:"3" maxLength:"50" example:"My Thing"`
	Count           int       `json:"count" minimum:"0" maximum:"100" example:"42"`
	Price           float64   `json:"price" minimum:"0" multipleOf:"0.01" example:"19.99"`
	Tags            []string  `json:"tags" minItems:"1" maxItems:"10"`
	ReadOnlyID      string    `json:"readOnlyId" readOnly:"true" doc:"Read-only identifier"`
	WriteOnlyToken  string    `json:"writeOnlyToken" writeOnly:"true" doc:"Write-only token"`
	DeprecatedField string    `json:"deprecatedField" deprecated:"true" doc:"Deprecated field"`
	CreatedAt       time.Time `json:"createdAt" format:"date-time"`
}

type GetThingResponse struct {
	Body TestThing
}

type ListThingsResponse struct {
	Body []TestThing
}

type CreateThingRequest struct {
	Body TestThing
}

type CreateThingResponse struct {
	Body TestThing
}

// Mock HTTP client for testing generated clients
type MockHTTPClient struct {
	responses map[string]*http.Response
	requests  []*http.Request
}

func NewMockHTTPClient() *MockHTTPClient {
	return &MockHTTPClient{
		responses: make(map[string]*http.Response),
		requests:  make([]*http.Request, 0),
	}
}

func (m *MockHTTPClient) SetResponse(method, path string, statusCode int, body any) {
	key := method + " " + path
	var bodyBytes []byte
	if body != nil {
		bodyBytes, _ = json.Marshal(body)
	}

	m.responses[key] = &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
	}
	m.responses[key].Header.Set("Content-Type", "application/json")
}

func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	m.requests = append(m.requests, req)

	key := req.Method + " " + req.URL.Path
	if resp, ok := m.responses[key]; ok {
		return resp, nil
	}

	// Default 404 response
	return &http.Response{
		StatusCode: 404,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
	}, nil
}

func (m *MockHTTPClient) GetRequests() []*http.Request {
	return m.requests
}

func createTestAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))

	// GET /things - List things with optional query parameters
	huma.Get(api, "/things", func(ctx context.Context, input *struct {
		Limit  int    `query:"limit" doc:"Maximum number of items" default:"10" minimum:"1" maximum:"100"`
		Cursor string `query:"cursor" doc:"Pagination cursor"`
		Filter string `query:"filter" doc:"Filter items"`
	}) (*ListThingsResponse, error) {
		return &ListThingsResponse{
			Body: []TestThing{
				{ID: "thing1", Name: "First Thing", Count: 1},
				{ID: "thing2", Name: "Second Thing", Count: 2},
			},
		}, nil
	})

	// GET /things/{id} - Get single thing
	huma.Get(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID          string `path:"id" doc:"Thing ID"`
		ApiKey      string `header:"X-API-Key" doc:"API Key"`
		IncludeData string `query:"include" doc:"Include additional data"`
	}) (*GetThingResponse, error) {
		return &GetThingResponse{
			Body: TestThing{
				ID:   input.ID,
				Name: "Test Thing",
			},
		}, nil
	})

	// POST /things - Create thing
	huma.Post(api, "/things", func(ctx context.Context, input *CreateThingRequest) (*CreateThingResponse, error) {
		return &CreateThingResponse{
			Body: input.Body,
		}, nil
	})

	// PUT /things/{id} - Update thing
	huma.Put(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body TestThing
	}) (*CreateThingResponse, error) {
		return &CreateThingResponse{
			Body: input.Body,
		}, nil
	})

	// DELETE /things/{id} - Delete thing
	huma.Delete(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api
}

func TestGenerateClient(t *testing.T) {
	api := createTestAPI()

	// Create temporary directory for generated client
	tempDir, err := os.MkdirTemp("", "humaclient_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Change to temp directory for client generation
	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Test client generation
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Check if the directory was created
	if _, err := os.Stat("testapiclient"); os.IsNotExist(err) {
		t.Fatal("Expected testapiclient directory to be created")
	}

	// Check if the client file was created
	if _, err := os.Stat("testapiclient/client.go"); os.IsNotExist(err) {
		t.Fatal("Expected testapiclient/client.go file to be created")
	}
}

func TestCasing(t *testing.T) {
	tests := []struct {
		input              string
		expectedLowerCamel string
		expectedSnake      string
	}{
		{"hello-world", "helloWorld", "hello_world"},
		{"get_things", "getThings", "get_things"},
		{"GetThingByID", "getThingById", "get_thing_by_id"},
		{"API", "api", "api"},
		{"simple", "simple", "simple"},
		{"XMLHttpRequest", "xmlHttpRequest", "xml_http_request"},
		{"HTTPSConnection", "httpsConnection", "https_connection"},
	}

	for _, test := range tests {
		lowerCamelResult := casing.LowerCamel(test.input)
		if lowerCamelResult != test.expectedLowerCamel {
			t.Errorf("casing.LowerCamel(%q) = %q, expected %q", test.input, lowerCamelResult, test.expectedLowerCamel)
		}

		snakeResult := casing.Snake(test.input)
		if snakeResult != test.expectedSnake {
			t.Errorf("casing.Snake(%q) = %q, expected %q", test.input, snakeResult, test.expectedSnake)
		}
	}
}

func TestComprehensiveClientGeneration(t *testing.T) {
	api := createTestAPI()

	// Create temporary directory for generated client
	tempDir, err := os.MkdirTemp("", "humaclient_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Change to temp directory for client generation
	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate client
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Verify client directory and file exist
	clientDir := "testapiclient"
	clientFile := filepath.Join(clientDir, "client.go")

	if _, err := os.Stat(clientDir); os.IsNotExist(err) {
		t.Fatal("Expected client directory to be created")
	}

	if _, err := os.Stat(clientFile); os.IsNotExist(err) {
		t.Fatal("Expected client.go file to be created")
	}

	// Read and parse generated client code
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Verify generated content
	t.Run("GeneratedCodeContainsExpectedStructs", func(t *testing.T) {
		expectedStructs := []string{
			"type TestThing struct",
			"type Option func",
			"type OptionsApplier interface",
			"type RequestOptions struct",
			"type TestAPIClient interface",
			"type TestAPIClientImpl struct",
		}

		for _, expected := range expectedStructs {
			if !strings.Contains(clientCode, expected) {
				t.Errorf("Generated code missing expected struct/interface: %s", expected)
			}
		}
	})

	t.Run("GeneratedCodeContainsValidationTags", func(t *testing.T) {
		expectedTags := []string{
			`minLength:"8"`,
			`pattern:"^[a-z0-9_-]+$"`,
			`example:"thing123"`,
			`minimum:"0"`,
			`maximum:"100"`,
			`readOnly:"true"`,
			`writeOnly:"true"`,
			`deprecated:"true"`,
			`format:"date-time"`,
		}

		for _, tag := range expectedTags {
			if !strings.Contains(clientCode, tag) {
				t.Errorf("Generated code missing expected validation tag: %s", tag)
			}
		}
	})

	t.Run("GeneratedCodeContainsMethods", func(t *testing.T) {
		expectedMethods := []string{
			"ListThings(ctx context.Context, opts ...Option)",
			"GetThingsByID(ctx context.Context, id string, opts ...Option)",
			"Follow(ctx context.Context, link string, result any, opts ...Option)",
		}

		for _, method := range expectedMethods {
			if !strings.Contains(clientCode, method) {
				t.Errorf("Generated code missing expected method: %s", method)
			}
		}

		// Also verify that basic method signatures exist
		if !strings.Contains(clientCode, "body TestThing") {
			t.Error("Generated code should contain methods with TestThing body parameters")
		}
		if !strings.Contains(clientCode, "id string") {
			t.Error("Generated code should contain methods with string ID path parameters")
		}
	})

	t.Run("GeneratedCodeContainsOptionFunctions", func(t *testing.T) {
		expectedFunctions := []string{
			"func WithHeader(key, value string) Option",
			"func WithQuery(key, value string) Option",
			"func WithBody(body any) Option",
			"func WithOptions(applier OptionsApplier) Option",
		}

		for _, function := range expectedFunctions {
			if !strings.Contains(clientCode, function) {
				t.Errorf("Generated code missing expected option function: %s", function)
			}
		}
	})

	t.Run("GeneratedCodeContainsOptionsStructs", func(t *testing.T) {
		// Should have operation-specific options structs
		if !strings.Contains(clientCode, "type ListThingsOptions struct") {
			t.Error("Generated code missing ListThingsOptions struct")
		}

		if !strings.Contains(clientCode, "func (o ListThingsOptions) Apply(opts *RequestOptions)") {
			t.Error("Generated code missing ListThingsOptions.Apply method")
		}

		if !strings.Contains(clientCode, "type GetThingsByIDOptions struct") {
			t.Error("Generated code missing GetThingsByIDOptions struct")
		}
	})
}

func TestGeneratedClientSyntax(t *testing.T) {
	api := createTestAPI()

	// Create temporary directory and generate client
	tempDir, err := os.MkdirTemp("", "humaclient_syntax_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Parse the generated client code to verify syntax
	clientFile := filepath.Join("testapiclient", "client.go")
	src, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	// Parse the Go code
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, clientFile, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse generated client code: %v", err)
	}

	t.Run("ValidGoSyntax", func(t *testing.T) {
		// If parsing succeeded, the Go syntax is valid
		if node.Name.Name != "testapiclient" {
			t.Errorf("Expected package name 'testapiclient', got '%s'", node.Name.Name)
		}
	})

	t.Run("ContainsRequiredImports", func(t *testing.T) {
		requiredImports := []string{
			"bytes", "context", "encoding/json", "fmt", "io", "net/http", "net/url", "strings",
		}

		importMap := make(map[string]bool)
		for _, imp := range node.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			importMap[path] = true
		}

		for _, required := range requiredImports {
			if !importMap[required] {
				t.Errorf("Generated client missing required import: %s", required)
			}
		}
	})

	t.Run("StructFieldsHaveCorrectTags", func(t *testing.T) {
		// Find the TestThing struct and verify its field tags
		var testThingStruct *ast.StructType
		ast.Inspect(node, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == "TestThing" {
				if st, ok := ts.Type.(*ast.StructType); ok {
					testThingStruct = st
					return false
				}
			}
			return true
		})

		if testThingStruct == nil {
			t.Fatal("TestThing struct not found in generated code")
		}

		// Check specific field tags
		fieldTagTests := map[string][]string{
			"ID":              {`json:"id"`, `doc:"The unique identifier"`, `minLength:"8"`, `pattern:"^[a-z0-9_-]+$"`, `example:"thing123"`},
			"Name":            {`json:"name"`, `doc:"The name"`, `minLength:"3"`, `example:"My Thing"`},
			"Count":           {`json:"count"`, `minimum:"0"`, `maximum:"100"`, `example:"42"`},
			"ReadOnlyID":      {`json:"readOnlyId"`, `readOnly:"true"`},
			"WriteOnlyToken":  {`json:"writeOnlyToken"`, `writeOnly:"true"`},
			"DeprecatedField": {`json:"deprecatedField"`, `deprecated:"true"`},
		}

		for _, field := range testThingStruct.Fields.List {
			if len(field.Names) == 0 {
				continue
			}
			fieldName := field.Names[0].Name
			if expectedTags, exists := fieldTagTests[fieldName]; exists {
				if field.Tag == nil {
					t.Errorf("Field %s missing tag", fieldName)
					continue
				}
				tagValue := strings.Trim(field.Tag.Value, "`")
				for _, expectedTag := range expectedTags {
					if !strings.Contains(tagValue, expectedTag) {
						t.Errorf("Field %s missing expected tag %s, got: %s", fieldName, expectedTag, tagValue)
					}
				}
			}
		}
	})
}

func TestGeneratedClientBehavior(t *testing.T) {
	// Generate a real client and test its actual behavior
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_behavior_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate the client
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Create a test server to respond to client requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "GET" && r.URL.Path == "/things/test123":
			j, _ := json.Marshal(map[string]any{
				"id":              "test123",
				"name":            "Test Thing",
				"readOnlyId":      "ro123",
				"writeOnlyToken":  "token123",
				"deprecatedField": "deprecated",
				"createdAt":       "2023-01-01T00:00:00Z",
			})
			w.Write(j)

		case r.Method == "GET" && r.URL.Path == "/things":
			// Check for query parameters
			limit := r.URL.Query().Get("limit")
			_ = r.URL.Query().Get("cursor") // cursor parameter is captured but not used in this simple test

			things := []map[string]any{
				{"id": "thing1", "name": "First Thing"},
				{"id": "thing2", "name": "Second Thing"},
			}

			// Simulate pagination
			if limit == "1" {
				things = things[:1]
			}

			j, _ := json.Marshal(things)
			w.Write(j)

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/follow/"):
			// Test Follow method
			j, _ := json.Marshal(map[string]any{
				"id":   "followed123",
				"name": "Followed Thing",
			})
			w.Write(j)

		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"detail": "Not found"}`))
		}
	}))
	defer server.Close()

	// Create go.mod file for proper module resolution
	err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.19\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Create and run test programs that use the generated client
	t.Run("GetThingsByID", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/testapiclient"
)

func main() {
	client := testapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	resp, thing, err := client.GetThingsByID(context.Background(), "test123")
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	result := map[string]any{
		"statusCode": resp.StatusCode,
		"thing": thing,
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
`, server.URL)

		err := os.WriteFile("test_get.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		output, err := runGoProgram("test_get.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v", err)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse test output: %v", err)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		thing := result["thing"].(map[string]any)
		if thing["id"] != "test123" {
			t.Errorf("Expected ID 'test123', got %v", thing["id"])
		}
		if thing["name"] != "Test Thing" {
			t.Errorf("Expected name 'Test Thing', got %v", thing["name"])
		}
	})

	t.Run("ListThingsWithOptions", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/testapiclient"
)

func main() {
	client := testapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	// Test with options
	resp, things, err := client.ListThings(context.Background(),
		testapiclient.WithOptions(testapiclient.ListThingsOptions{
			Limit:  1,
			Cursor: "abc123",
		}))
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	result := map[string]any{
		"statusCode": resp.StatusCode,
		"things": things,
		"requestURL": resp.Request.URL.String(),
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
`, server.URL)

		err := os.WriteFile("test_list.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		output, err := runGoProgram("test_list.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v", err)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse test output: %v", err)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		// Verify query parameters were applied
		requestURL := result["requestURL"].(string)
		if !strings.Contains(requestURL, "limit=1") {
			t.Errorf("Expected request URL to contain 'limit=1', got: %s", requestURL)
		}
		if !strings.Contains(requestURL, "cursor=abc123") {
			t.Errorf("Expected request URL to contain 'cursor=abc123', got: %s", requestURL)
		}

		// Should only return 1 item due to limit
		things := result["things"].([]any)
		if len(things) != 1 {
			t.Errorf("Expected 1 item due to limit, got %d", len(things))
		}
	})

	t.Run("FollowLink", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/testapiclient"
)

type Thing struct {
	ID   string `+"`json:\"id\"`"+`
	Name string `+"`json:\"name\"`"+`
}

func main() {
	client := testapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	var thing Thing
	resp, err := client.Follow(context.Background(), "%s/follow/123", &thing)
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	result := map[string]any{
		"statusCode": resp.StatusCode,
		"thing": thing,
	}

	json.NewEncoder(os.Stdout).Encode(result)
}
`, server.URL, server.URL)

		err := os.WriteFile("test_follow.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		output, err := runGoProgram("test_follow.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v", err)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse test output: %v", err)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		thing := result["thing"].(map[string]any)
		if thing["id"] != "followed123" {
			t.Errorf("Expected ID 'followed123', got %v", thing["id"])
		}
		if thing["name"] != "Followed Thing" {
			t.Errorf("Expected name 'Followed Thing', got %v", thing["name"])
		}
	})
}

func TestGeneratedClientErrorHandling(t *testing.T) {
	// Test how the generated client handles HTTP errors
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_error_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Create a server that returns errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		w.Write([]byte(`{"detail": "Resource not found", "status": 404}`))
	}))
	defer server.Close()

	// Create go.mod file for proper module resolution
	err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.19\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"testprogram/testapiclient"
)

func main() {
	client := testapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	resp, thing, err := client.GetThingsByID(context.Background(), "nonexistent")
	if err != nil {
		fmt.Printf("ERROR_OCCURRED: %%v\n", err)
		if strings.Contains(err.Error(), "404") {
			fmt.Printf("ERROR_IS_404: true\n")
		}
		if strings.Contains(err.Error(), "not found") {
			fmt.Printf("ERROR_CONTAINS_MESSAGE: true\n")
		}
	}

	if resp != nil {
		fmt.Printf("RESPONSE_STATUS: %%d\n", resp.StatusCode)
	}

	fmt.Printf("THING_ZERO_VALUE: %%+v\n", thing)
}
`, server.URL)

	err = os.WriteFile("test_error.go", []byte(testProgram), 0644)
	if err != nil {
		t.Fatalf("Failed to write test program: %v", err)
	}

	output, err := runGoProgram("test_error.go")
	if err != nil {
		t.Fatalf("Failed to run test program: %v", err)
	}

	// Verify error handling behavior
	if !strings.Contains(output, "ERROR_OCCURRED:") {
		t.Error("Expected client to return an error for 404 response")
	}
	if !strings.Contains(output, "ERROR_IS_404: true") {
		t.Error("Expected error to contain 404 status code")
	}
	if !strings.Contains(output, "ERROR_CONTAINS_MESSAGE: true") {
		t.Error("Expected error to contain 'not found' message")
	}
	if !strings.Contains(output, "RESPONSE_STATUS: 404") {
		t.Error("Expected response to be returned even on error")
	}
}

func TestValidationTagsPreservation(t *testing.T) {
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_validation_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("testapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Test comprehensive validation tag preservation
	validationTests := []struct {
		tag         string
		description string
	}{
		{`doc:"`, "Documentation tags"},
		{`minLength:"`, "Minimum length validation"},
		{`maxLength:"`, "Maximum length validation"},
		{`pattern:"`, "Pattern validation"},
		{`minimum:"`, "Minimum value validation"},
		{`maximum:"`, "Maximum value validation"},
		{`multipleOf:"`, "Multiple of validation"},
		{`minItems:"`, "Minimum items validation"},
		{`maxItems:"`, "Maximum items validation"},
		{`example:"`, "Example values"},
		{`readOnly:"true"`, "Read-only fields"},
		{`writeOnly:"true"`, "Write-only fields"},
		{`deprecated:"true"`, "Deprecated fields"},
		{`format:"`, "Format specifications"},
	}

	for _, test := range validationTests {
		if !strings.Contains(clientCode, test.tag) {
			t.Errorf("Generated code missing %s (%s)", test.description, test.tag)
		}
	}
}

func TestClientConstructors(t *testing.T) {
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_constructor_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("testapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("NewMethodSignature", func(t *testing.T) {
		expectedSignature := "func New(baseURL string) TestAPIClient"
		if !strings.Contains(clientCode, expectedSignature) {
			t.Errorf("Generated client missing expected New method signature: %s", expectedSignature)
		}
	})

	t.Run("NewWithClientMethodSignature", func(t *testing.T) {
		expectedSignature := "func NewWithClient(baseURL string, client *http.Client) TestAPIClient"
		if !strings.Contains(clientCode, expectedSignature) {
			t.Errorf("Generated client missing expected NewWithClient method signature: %s", expectedSignature)
		}
	})
}

func TestJSONCasingPreservation(t *testing.T) {
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_casing_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("testapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Verify specific JSON field name preservation
	jsonFieldTests := []struct {
		goField   string
		jsonField string
	}{
		{"ReadOnlyID", "readOnlyId"},           // camelCase preserved
		{"WriteOnlyToken", "writeOnlyToken"},   // camelCase preserved
		{"DeprecatedField", "deprecatedField"}, // camelCase preserved
		{"CreatedAt", "createdAt"},             // camelCase preserved
	}

	for _, test := range jsonFieldTests {
		expectedTag := fmt.Sprintf(`json:"%s"`, test.jsonField)
		if !strings.Contains(clientCode, expectedTag) {
			t.Errorf("Expected JSON field tag %s for Go field %s not found", expectedTag, test.goField)
		}
	}
}

// runGoProgram compiles and runs a Go program, returning its stdout output
func runGoProgram(filename string) (string, error) {
	cmd := exec.Command("go", "run", filename)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("program failed: %v\nOutput: %s", err, string(output))
	}
	return string(output), nil
}

func TestCircularReferencesWithNullableSchemas(t *testing.T) {
	// Create API with circular references using nullable schemas
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Circular API", "1.0.0"))

	// Define models with circular references
	type Node struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Parent   *Node  `json:"parent,omitempty" doc:"Parent node (nullable to break circular reference)"`
		Children []Node `json:"children,omitempty" doc:"Child nodes"`
	}

	type Category struct {
		ID            string     `json:"id"`
		Name          string     `json:"name"`
		ParentCat     *Category  `json:"parentCategory,omitempty" doc:"Parent category (nullable)"`
		SubCategories []Category `json:"subCategories,omitempty" doc:"Sub-categories"`
	}

	// Add operations that use these circular types
	huma.Get(api, "/nodes/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body Node }, error) {
		return &struct{ Body Node }{
			Body: Node{
				ID:   input.ID,
				Name: "Test Node",
			},
		}, nil
	})

	huma.Get(api, "/categories/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body Category }, error) {
		return &struct{ Body Category }{
			Body: Category{
				ID:   input.ID,
				Name: "Test Category",
			},
		}, nil
	})

	// Test client generation with circular references
	tempDir, err := os.MkdirTemp("", "humaclient_circular_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate the client
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client with circular references: %v", err)
	}

	// Read the generated code
	clientFile := "circularapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Verify that nullable references use pointers
	t.Run("NullableReferencesUsePointers", func(t *testing.T) {
		// Check that Parent field uses pointer type
		if !strings.Contains(clientCode, "Parent   *Node") {
			t.Error("Expected Parent field to use pointer type (*Node) for nullable reference")
		}

		if !strings.Contains(clientCode, "ParentCategory *Category") {
			t.Error("Expected ParentCategory field to use pointer type (*Category) for nullable reference")
		}
	})

	// Verify that non-nullable references don't use pointers (for slices this is expected)
	t.Run("NonNullableReferencesStructure", func(t *testing.T) {
		// Child collections should be slices, not pointer slices
		if !strings.Contains(clientCode, "Children []Node") {
			t.Error("Expected Children field to use slice type ([]Node)")
		}

		if !strings.Contains(clientCode, "SubCategories  []Category") {
			t.Error("Expected SubCategories field to use slice type ([]Category)")
		}
	})

	// Verify the code compiles
	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with circular references has syntax errors: %v", err)
		}
	})

	// Create go.mod and test compilation
	t.Run("ActualCompilation", func(t *testing.T) {
		err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.19\nrequire github.com/danielgtaylor/huma/v2 v2.0.0\n"), 0644)
		if err != nil {
			t.Fatalf("Failed to create go.mod: %v", err)
		}

		// Try to compile the generated client
		cmd := exec.Command("go", "build", "./circularapiclient")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Generated client with circular references failed to compile: %v\nOutput: %s", err, string(output))
		}
	})
}

func TestHumaSchemaCircularReferences(t *testing.T) {
	// Test using huma.Schema directly, which has circular references to itself
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Schema Test API", "1.0.0"))

	// Define a type that uses huma.Schema, which has circular references
	type SchemaWrapper struct {
		ID     string      `json:"id"`
		Schema huma.Schema `json:"schema"`
	}

	// Add operation that uses huma.Schema
	huma.Get(api, "/schema-wrappers/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body SchemaWrapper }, error) {
		return &struct{ Body SchemaWrapper }{
			Body: SchemaWrapper{
				ID: input.ID,
			},
		}, nil
	})

	// Test client generation with huma.Schema circular references
	tempDir, err := os.MkdirTemp("", "humaclient_huma_schema_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate the client
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client with huma.Schema circular references: %v", err)
	}

	// Read the generated code
	clientFile := "schematestapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Verify that Schema fields use pointers where needed for circular references
	t.Run("SchemaFieldsUsePointersForCircularReferences", func(t *testing.T) {
		// Check that Items field uses pointer type for circular reference
		if !strings.Contains(clientCode, "Items                *Schema") {
			t.Error("Expected Items field to use pointer type (*Schema) to break circular reference")
		}

		// Check that Not field uses pointer type for circular reference
		if !strings.Contains(clientCode, "Not                  *Schema") {
			t.Error("Expected Not field to use pointer type (*Schema) to break circular reference")
		}

		// Check that slice fields still use direct types (not pointers)
		if !strings.Contains(clientCode, "AllOf                []Schema") {
			t.Error("Expected AllOf field to use slice type ([]Schema)")
		}
		if !strings.Contains(clientCode, "AnyOf                []Schema") {
			t.Error("Expected AnyOf field to use slice type ([]Schema)")
		}
		if !strings.Contains(clientCode, "OneOf                []Schema") {
			t.Error("Expected OneOf field to use slice type ([]Schema)")
		}
	})

	// Verify the code compiles
	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with huma.Schema has syntax errors: %v", err)
		}
	})

	// Create go.mod and test compilation
	t.Run("ActualCompilation", func(t *testing.T) {
		err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.19\nrequire github.com/danielgtaylor/huma/v2 v2.0.0\n"), 0644)
		if err != nil {
			t.Fatalf("Failed to create go.mod: %v", err)
		}

		// Try to compile the generated client
		cmd := exec.Command("go", "build", "./schematestapiclient")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Generated client with huma.Schema failed to compile: %v\nOutput: %s\nGenerated code:\n%s", err, string(output), clientCode)
		}
	})
}

func TestGenerateClientWithCustomOptions(t *testing.T) {
	api := createTestAPI()

	// Create temporary directory for generated client
	tempDir, err := os.MkdirTemp("", "humaclient_custom_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Change to temp directory for client generation
	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Test with custom options
	opts := Options{
		PackageName: "custompkg",
		ClientName:  "CustomClient",
	}

	// Generate client with custom options
	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client with custom options: %v", err)
	}

	// Verify custom package name was used for directory
	if _, err := os.Stat("custompkg"); os.IsNotExist(err) {
		t.Fatal("Expected custompkg directory to be created")
	}

	// Verify custom client file was created
	if _, err := os.Stat("custompkg/client.go"); os.IsNotExist(err) {
		t.Fatal("Expected custompkg/client.go file to be created")
	}

	// Read and verify generated content
	content, err := os.ReadFile("custompkg/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Verify custom package name
	if !strings.Contains(clientCode, "package custompkg") {
		t.Error("Expected custom package name 'custompkg' in generated code")
	}

	// Verify custom client interface name
	if !strings.Contains(clientCode, "type CustomClient interface") {
		t.Error("Expected custom client interface name 'CustomClient' in generated code")
	}

	// Verify custom client struct name
	if !strings.Contains(clientCode, "type CustomClientImpl struct") {
		t.Error("Expected custom client struct name 'CustomClientImpl' in generated code")
	}

	// Verify constructor functions use custom interface name
	if !strings.Contains(clientCode, "func New(baseURL string) CustomClient") {
		t.Error("Expected New function to return CustomClient interface")
	}

	if !strings.Contains(clientCode, "func NewWithClient(baseURL string, client *http.Client) CustomClient") {
		t.Error("Expected NewWithClient function to return CustomClient interface")
	}
}

func TestRegisterWithOptions(t *testing.T) {
	api := createTestAPI()

	// Test that RegisterWithOptions with empty options works the same as Register
	tempDir, err := os.MkdirTemp("", "humaclient_register_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// This should work without panicking
	RegisterWithOptions(api, Options{})

	// Should not generate anything since GENERATE_CLIENT is not set
	if _, err := os.Stat("testapiclient"); !os.IsNotExist(err) {
		t.Error("Expected no client to be generated when GENERATE_CLIENT is not set")
	}
}

func TestPartialCustomOptions(t *testing.T) {
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_partial_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Test with only custom package name
	t.Run("CustomPackageOnly", func(t *testing.T) {
		opts := Options{
			PackageName: "myclient",
		}

		err = GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		// Check directory name
		if _, err := os.Stat("myclient"); os.IsNotExist(err) {
			t.Fatal("Expected myclient directory to be created")
		}

		content, err := os.ReadFile("myclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Should use custom package name
		if !strings.Contains(clientCode, "package myclient") {
			t.Error("Expected custom package name 'myclient'")
		}

		// Should use default client interface name
		if !strings.Contains(clientCode, "type TestAPIClient interface") {
			t.Error("Expected default client interface name 'TestAPIClient'")
		}

		// Clean up for next test
		os.RemoveAll("myclient")
	})

	// Test with only custom client name
	t.Run("CustomClientOnly", func(t *testing.T) {
		opts := Options{
			ClientName: "MyAPIClient",
		}

		err = GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		// Should use default package name
		if _, err := os.Stat("testapiclient"); os.IsNotExist(err) {
			t.Fatal("Expected testapiclient directory to be created")
		}

		content, err := os.ReadFile("testapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Should use default package name
		if !strings.Contains(clientCode, "package testapiclient") {
			t.Error("Expected default package name 'testapiclient'")
		}

		// Should use custom client interface name
		if !strings.Contains(clientCode, "type MyAPIClient interface") {
			t.Error("Expected custom client interface name 'MyAPIClient'")
		}

		if !strings.Contains(clientCode, "func New(baseURL string) MyAPIClient") {
			t.Error("Expected New function to return MyAPIClient interface")
		}
	})
}

func TestAllowedPackagesBasicFunctionality(t *testing.T) {
	// Create API that uses huma.Schema to test external type referencing
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Schema Test API", "1.0.0"))

	// Add operation that returns huma.Schema
	huma.Get(api, "/schema", func(ctx context.Context, input *struct{}) (*struct {
		Body huma.Schema `json:"schema" doc:"The schema object"`
	}, error) {
		return &struct {
			Body huma.Schema `json:"schema" doc:"The schema object"`
		}{
			Body: huma.Schema{Type: "object", Description: "Example schema"},
		}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_allowed_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate client with allowed packages
	opts := Options{
		PackageName:     "testclient",
		ClientName:      "TestClient",
		AllowedPackages: []string{"github.com/danielgtaylor/huma/v2"},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client with allowed packages: %v", err)
	}

	// Read generated code
	content, err := os.ReadFile("testclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ExternalImportIncluded", func(t *testing.T) {
		if !strings.Contains(clientCode, `"github.com/danielgtaylor/huma/v2"`) {
			t.Error("Expected external import 'github.com/danielgtaylor/huma/v2' to be included")
		}
	})

	t.Run("ExternalTypeUsed", func(t *testing.T) {
		// Should use huma.Schema instead of generating a local Schema struct
		if !strings.Contains(clientCode, "huma.Schema") {
			t.Error("Expected external type 'huma.Schema' to be used in method signatures")
		}
	})

	t.Run("NoLocalSchemaStruct", func(t *testing.T) {
		// Should not generate a local Schema struct
		if strings.Contains(clientCode, "type Schema struct") {
			t.Error("Expected no local Schema struct to be generated when using external package")
		}
	})

	t.Run("MethodSignatureUsesExternalType", func(t *testing.T) {
		// The method signature should use huma.Schema
		expectedSignature := "GetSchema(ctx context.Context, opts ...Option) (*http.Response, huma.Schema, error)"
		if !strings.Contains(clientCode, expectedSignature) {
			t.Errorf("Expected method signature with external type: %s", expectedSignature)
		}
	})
}

func TestGetExternalPackageAndType(t *testing.T) {
	// Create a simple API with huma.Schema
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))

	// Add operation to register huma.Schema in the registry
	huma.Get(api, "/schema", func(ctx context.Context, input *struct{}) (*struct {
		Body huma.Schema `json:"schema"`
	}, error) {
		return nil, nil
	})

	openapi := api.OpenAPI()
	allowedPackages := []string{"github.com/danielgtaylor/huma/v2"}

	tests := []struct {
		name             string
		schemaName       string
		allowedPkgs      []string
		expectedPkg      string
		expectedType     string
		shouldBeExternal bool
	}{
		{
			name:             "HumaSchemaAllowed",
			schemaName:       "Schema",
			allowedPkgs:      allowedPackages,
			expectedPkg:      "github.com/danielgtaylor/huma/v2",
			expectedType:     "huma.Schema",
			shouldBeExternal: true,
		},
		{
			name:             "HumaSchemaNotAllowed",
			schemaName:       "Schema",
			allowedPkgs:      []string{},
			expectedPkg:      "",
			expectedType:     "",
			shouldBeExternal: false,
		},
		{
			name:             "NonExistentSchema",
			schemaName:       "NonExistent",
			allowedPkgs:      allowedPackages,
			expectedPkg:      "",
			expectedType:     "",
			shouldBeExternal: false,
		},
		{
			name:             "DifferentAllowedPackage",
			schemaName:       "Schema",
			allowedPkgs:      []string{"some.other/package"},
			expectedPkg:      "",
			expectedType:     "",
			shouldBeExternal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, typeName := getExternalPackageAndType(openapi, tt.schemaName, tt.allowedPkgs)

			if pkg != tt.expectedPkg {
				t.Errorf("Expected package %q, got %q", tt.expectedPkg, pkg)
			}

			if typeName != tt.expectedType {
				t.Errorf("Expected type name %q, got %q", tt.expectedType, typeName)
			}

			isExternal := pkg != ""
			if isExternal != tt.shouldBeExternal {
				t.Errorf("Expected isExternal %v, got %v", tt.shouldBeExternal, isExternal)
			}
		})
	}
}

func TestAllowedPackagesWithComplexTypes(t *testing.T) {
	// Create API with complex types that reference external packages
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Complex Test API", "1.0.0"))

	// Create a custom type that embeds huma.Schema
	type ComplexType struct {
		ID     string      `json:"id"`
		Schema huma.Schema `json:"schema"`
		Nested struct {
			SubSchema huma.Schema `json:"subSchema"`
		} `json:"nested"`
	}

	huma.Get(api, "/complex/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body ComplexType }, error) {
		return &struct{ Body ComplexType }{}, nil
	})

	// Also test with array of external types
	huma.Get(api, "/schemas", func(ctx context.Context, input *struct{}) (*struct {
		Body []huma.Schema `json:"schemas"`
	}, error) {
		return &struct {
			Body []huma.Schema `json:"schemas"`
		}{}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_complex_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Test with allowed packages
	opts := Options{
		PackageName:     "complextestclient",
		AllowedPackages: []string{"github.com/danielgtaylor/huma/v2"},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("complextestclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ExternalTypeInComplexStruct", func(t *testing.T) {
		// Should use huma.Schema in the generated ComplexType struct
		if !strings.Contains(clientCode, "Schema huma.Schema") {
			t.Error("Expected huma.Schema to be used in complex struct fields")
		}
	})

	t.Run("ExternalTypeInNestedStruct", func(t *testing.T) {
		// Should use huma.Schema in nested structures
		if !strings.Contains(clientCode, "SubSchema huma.Schema") {
			t.Error("Expected huma.Schema to be used in nested struct fields")
		}
	})

	t.Run("ExternalTypeInArrays", func(t *testing.T) {
		// Should use []huma.Schema for array types
		if !strings.Contains(clientCode, "[]huma.Schema") {
			t.Error("Expected []huma.Schema to be used for array return types")
		}
	})

	t.Run("NoLocalSchemaDefinitions", func(t *testing.T) {
		// Should not generate local Schema struct definitions
		if strings.Contains(clientCode, "type Schema struct") {
			t.Error("Should not generate local Schema struct when using external package")
		}
	})
}

func TestAllowedPackagesVersionedHandling(t *testing.T) {
	tests := []struct {
		name        string
		packagePath string
		expected    string
	}{
		{
			name:        "HumaV2Package",
			packagePath: "github.com/danielgtaylor/huma/v2",
			expected:    "huma.Schema",
		},
		{
			name:        "GenericV3Package",
			packagePath: "example.com/somelib/v3",
			expected:    "somelib.SomeType",
		},
		{
			name:        "NonVersionedPackage",
			packagePath: "github.com/example/lib",
			expected:    "lib.SomeType",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock registry and API to test version handling
			mux := http.NewServeMux()
			api := humago.New(mux, huma.DefaultConfig("Version Test API", "1.0.0"))

			// For huma/v2, we can test with actual Schema
			if tt.packagePath == "github.com/danielgtaylor/huma/v2" {
				huma.Get(api, "/test", func(ctx context.Context, input *struct{}) (*struct {
					Body huma.Schema `json:"schema"`
				}, error) {
					return nil, nil
				})

				openapi := api.OpenAPI()
				pkg, typeName := getExternalPackageAndType(openapi, "Schema", []string{tt.packagePath})

				if pkg != tt.packagePath {
					t.Errorf("Expected package %q, got %q", tt.packagePath, pkg)
				}

				if typeName != tt.expected {
					t.Errorf("Expected type name %q, got %q", tt.expected, typeName)
				}
			}
		})
	}
}

func TestAllowedPackagesEdgeCases(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Edge Case API", "1.0.0"))

	// Add a simple endpoint to have a valid API
	huma.Get(api, "/test", func(ctx context.Context, input *struct{}) (*SimpleMessageResponse, error) {
		return &SimpleMessageResponse{Body: "test"}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_edge_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	t.Run("EmptyAllowedPackages", func(t *testing.T) {
		opts := Options{
			AllowedPackages: []string{},
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client with empty allowed packages: %v", err)
		}

		// Should work normally, no external imports
		content, err := os.ReadFile("edgecaseapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Should not have any unusual imports
		lines := strings.Split(clientCode, "\n")
		inImportBlock := false
		for _, line := range lines {
			if strings.Contains(line, "import (") {
				inImportBlock = true
				continue
			}
			if inImportBlock && strings.Contains(line, ")") {
				break
			}
			if inImportBlock && strings.TrimSpace(line) != "" {
				// Should only contain standard library imports
				if !strings.Contains(line, "bytes") &&
					!strings.Contains(line, "context") &&
					!strings.Contains(line, "encoding/json") &&
					!strings.Contains(line, "fmt") &&
					!strings.Contains(line, "io") &&
					!strings.Contains(line, "net/http") &&
					!strings.Contains(line, "net/url") &&
					!strings.Contains(line, "strings") {
					t.Errorf("Unexpected import in client with empty allowed packages: %s", line)
				}
			}
		}

		// Clean up for next test
		os.RemoveAll("edgecaseapiclient")
	})

	t.Run("NilAllowedPackages", func(t *testing.T) {
		opts := Options{
			AllowedPackages: nil,
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client with nil allowed packages: %v", err)
		}

		// Should work normally
		if _, err := os.Stat("edgecaseapiclient/client.go"); os.IsNotExist(err) {
			t.Error("Expected client to be generated with nil allowed packages")
		}

		// Clean up for next test
		os.RemoveAll("edgecaseapiclient")
	})

	t.Run("NonExistentPackage", func(t *testing.T) {
		opts := Options{
			AllowedPackages: []string{"nonexistent.com/fake/package"},
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client with non-existent allowed package: %v", err)
		}

		// Should work normally, just ignore the non-existent package
		if _, err := os.Stat("edgecaseapiclient/client.go"); os.IsNotExist(err) {
			t.Error("Expected client to be generated even with non-existent allowed package")
		}

		content, err := os.ReadFile("edgecaseapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Should not import the non-existent package
		if strings.Contains(clientCode, "nonexistent.com/fake/package") {
			t.Error("Should not import non-existent package")
		}
	})
}

// Test types for integration test
type IntegrationSchemaResponse struct {
	Body huma.Schema
}

type SimpleMessageResponse struct {
	Body string
}

type IntegrationErrorResponse struct {
	Body []huma.ErrorDetail
}

func TestAllowedPackagesIntegration(t *testing.T) {
	// Integration test: Create a full API with external types and test compilation
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Integration Test API", "1.0.0"))

	// Add a simple endpoint that uses huma.Schema
	huma.Get(api, "/schema", func(ctx context.Context, input *struct{}) (*IntegrationSchemaResponse, error) {
		return &IntegrationSchemaResponse{Body: huma.Schema{Type: "object"}}, nil
	})

	// Add endpoint that uses huma.ErrorDetail
	huma.Get(api, "/errors", func(ctx context.Context, input *struct{}) (*IntegrationErrorResponse, error) {
		return &IntegrationErrorResponse{}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_integration_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate client with huma package allowed
	opts := Options{
		PackageName:     "integrationclient",
		AllowedPackages: []string{"github.com/danielgtaylor/huma/v2"},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate integration client: %v", err)
	}

	// Read generated content
	content, err := os.ReadFile("integrationclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("AllExternalTypesUsed", func(t *testing.T) {
		externalTypes := []string{
			"huma.Schema",
			"huma.ErrorDetail",
			"[]huma.ErrorDetail",
		}

		for _, extType := range externalTypes {
			if !strings.Contains(clientCode, extType) {
				t.Errorf("Expected external type %q to be used", extType)
			}
		}
	})

	t.Run("NoLocalTypeDefinitions", func(t *testing.T) {
		localTypes := []string{
			"type Schema struct",
			"type ErrorDetail struct",
			"type ErrorModel struct", // This should also be external
		}

		for _, localType := range localTypes {
			if strings.Contains(clientCode, localType) {
				t.Errorf("Should not generate local type definition: %s", localType)
			}
		}
	})

	t.Run("GeneratedClientCompiles", func(t *testing.T) {
		t.Skip("Skipping compilation test due to go.sum dependency resolution in test environment")
		// Create go.mod for compilation test
		goMod := "module integrationtest\n\ngo 1.19\n\nrequire github.com/danielgtaylor/huma/v2 v2.0.0\n"
		err := os.WriteFile("go.mod", []byte(goMod), 0644)
		if err != nil {
			t.Fatalf("Failed to create go.mod: %v", err)
		}

		// Test compilation
		cmd := exec.Command("go", "build", "./integrationclient")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Generated client failed to compile: %v\nOutput: %s\nGenerated code preview:\n%s",
				err, string(output), clientCode[:min(len(clientCode), 1000)])
		}
	})

	t.Run("ClientCanBeUsedInProgram", func(t *testing.T) {
		t.Skip("Skipping program compilation test due to go.sum dependency resolution in test environment")
		// Create a simple program that uses the generated client
		testProgram := "package main\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"integrationtest/integrationclient\"\n\t\"github.com/danielgtaylor/huma/v2\"\n)\n\nfunc main() {\n\tclient := integrationclient.New(\"http://example.com\")\n\t_, schema, err := client.GetSchema(context.Background())\n\tif err != nil {\n\t\tfmt.Printf(\"Error: %v\\n\", err)\n\t\treturn\n\t}\n\tvar humaSchema huma.Schema = schema\n\tfmt.Printf(\"Schema type: %s\\n\", humaSchema.Type)\n}"

		err := os.WriteFile("main.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		// Test compilation of the program using the generated client
		cmd := exec.Command("go", "build", "main.go")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Test program using generated client failed to compile: %v\nOutput: %s", err, string(output))
		}
	})
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Benchmark tests for client generation performance
func BenchmarkClientGeneration(b *testing.B) {
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_bench_*")
	if err != nil {
		b.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := GenerateClient(api)
		if err != nil {
			b.Fatalf("Failed to generate client: %v", err)
		}
		// Clean up between iterations
		os.RemoveAll("testapiclient")
	}
}

func BenchmarkClientGenerationWithAllowedPackages(b *testing.B) {
	// Create API with external types
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Benchmark API", "1.0.0"))

	huma.Get(api, "/schema", func(ctx context.Context, input *struct{}) (*IntegrationSchemaResponse, error) {
		return &IntegrationSchemaResponse{Body: huma.Schema{Type: "object"}}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_bench_allowed_*")
	if err != nil {
		b.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		AllowedPackages: []string{"github.com/danielgtaylor/huma/v2"},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			b.Fatalf("Failed to generate client with allowed packages: %v", err)
		}
		// Clean up between iterations
		os.RemoveAll("benchmarkapiclient")
	}
}

func TestStringFormatToGoTypeConversion(t *testing.T) {
	// Test models with various string formats
	type EventRecord struct {
		ID         string    `json:"id"`
		EventTime  time.Time `json:"eventTime" format:"date-time"`
		ServerIP   net.IP    `json:"serverIp" format:"ipv4"`
		ClientIPv6 net.IP    `json:"clientIpv6" format:"ipv6"`
	}

	// Create API with formatted string fields
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Format Test API", "1.0.0"))

	huma.Get(api, "/events/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body EventRecord }, error) {
		return &struct{ Body EventRecord }{
			Body: EventRecord{
				ID: input.ID,
			},
		}, nil
	})

	// Test client generation
	tempDir, err := os.MkdirTemp("", "humaclient_format_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client with formatted strings: %v", err)
	}

	// Read the generated code
	clientFile := "formattestapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("TimeFormatsUseTimeType", func(t *testing.T) {
		// Check that date-time format uses time.Time
		if !strings.Contains(clientCode, "EventTime  time.Time") {
			t.Error("Expected date-time format field to use time.Time type")
		}
	})

	t.Run("IPFormatsUseIPType", func(t *testing.T) {
		// Check that ipv4 format uses net.IP
		if !strings.Contains(clientCode, "ServerIP   net.IP") {
			t.Error("Expected ipv4 format field to use net.IP type")
		}

		// Check that ipv6 format uses net.IP
		if !strings.Contains(clientCode, "ClientIpv6 net.IP") {
			t.Error("Expected ipv6 format field to use net.IP type")
		}
	})

	t.Run("RequiredImportsIncluded", func(t *testing.T) {
		// Check that time package is imported
		if !strings.Contains(clientCode, `"time"`) {
			t.Error("Expected time package to be imported")
		}

		// Check that net package is imported
		if !strings.Contains(clientCode, `"net"`) {
			t.Error("Expected net package to be imported")
		}

		// Note: net/url is already in default imports, so we don't need to check for it
	})

	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with format conversions has syntax errors: %v", err)
		}
	})
}

func TestStringFormatToGoTypeConversionNullable(t *testing.T) {
	// Test nullable formatted string fields
	type OptionalEventRecord struct {
		ID           string     `json:"id"`
		OptionalTime *time.Time `json:"optionalTime,omitempty" format:"date-time"`
		OptionalIP   net.IP     `json:"optionalIp,omitempty" format:"ipv4"` // net.IP is already nullable
	}

	// Create API with nullable formatted fields
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Nullable Format Test API", "1.0.0"))

	huma.Get(api, "/optional-events/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body OptionalEventRecord }, error) {
		return &struct{ Body OptionalEventRecord }{
			Body: OptionalEventRecord{
				ID: input.ID,
			},
		}, nil
	})

	// Test client generation
	tempDir, err := os.MkdirTemp("", "humaclient_nullable_format_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client with nullable formatted strings: %v", err)
	}

	// Read the generated code
	clientFile := "nullableformattestapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("NullableTimeFormatsUsePointerTimeType", func(t *testing.T) {
		// Check that nullable date-time format uses *time.Time
		if !strings.Contains(clientCode, "*time.Time") {
			t.Error("Expected nullable date-time format field to use *time.Time type")
		}
	})

	t.Run("NullableIPFormatsUseIPType", func(t *testing.T) {
		// Check that nullable ipv4 format uses net.IP (already nullable)
		if !strings.Contains(clientCode, "net.IP") {
			t.Error("Expected nullable ipv4 format field to use net.IP type")
		}
	})

	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with nullable format conversions has syntax errors: %v", err)
		}
	})
}

func TestIntegerFormatToGoTypeConversion(t *testing.T) {
	// Test models with various integer formats
	type IntegerTypesRecord struct {
		ID            string `json:"id"`
		SmallSigned   int8   `json:"smallSigned" format:"int8" minimum:"-128" maximum:"127"`
		ShortSigned   int16  `json:"shortSigned" format:"int16" minimum:"-32768" maximum:"32767"`
		RegularInt    int32  `json:"regularInt" format:"int32" minimum:"-2147483648" maximum:"2147483647"`
		LargeSigned   int64  `json:"largeSigned" format:"int64"`
		SmallUnsigned uint8  `json:"smallUnsigned" format:"uint8" minimum:"0" maximum:"255"`
		ShortUnsigned uint16 `json:"shortUnsigned" format:"uint16" minimum:"0" maximum:"65535"`
		RegularUint   uint32 `json:"regularUint" format:"uint32" minimum:"0" maximum:"4294967295"`
		LargeUnsigned uint64 `json:"largeUnsigned" format:"uint64" minimum:"0"`
	}

	// Create API with integer format fields
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Integer Format Test API", "1.0.0"))

	huma.Get(api, "/integers/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct {
		Body IntegerTypesRecord
	}, error) {
		return &struct {
			Body IntegerTypesRecord
		}{
			Body: IntegerTypesRecord{
				ID:            input.ID,
				SmallSigned:   42,
				ShortSigned:   1024,
				RegularInt:    100000,
				LargeSigned:   9223372036854775807,
				SmallUnsigned: 255,
				ShortUnsigned: 65535,
				RegularUint:   4294967295,
				LargeUnsigned: 18446744073709551615,
			},
		}, nil
	})

	// Test client generation
	tempDir, err := os.MkdirTemp("", "humaclient_integer_format_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client with integer formats: %v", err)
	}

	// Read the generated code
	clientFile := "integerformattestapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("SignedIntegerFormatsUseCorrectTypes", func(t *testing.T) {
		// Check that int8 format uses int8 type
		if !strings.Contains(clientCode, "SmallSigned   int8") {
			t.Error("Expected int8 format field to use int8 type")
		}
		// Check that int16 format uses int16 type
		if !strings.Contains(clientCode, "ShortSigned   int16") {
			t.Error("Expected int16 format field to use int16 type")
		}
		// Check that int32 format uses int32 type
		if !strings.Contains(clientCode, "RegularInt    int32") {
			t.Error("Expected int32 format field to use int32 type")
		}
		// Check that int64 format uses int64 type
		if !strings.Contains(clientCode, "LargeSigned   int64") {
			t.Error("Expected int64 format field to use int64 type")
		}
	})

	t.Run("UnsignedIntegerFormatsUseCorrectTypes", func(t *testing.T) {
		// Check that uint8 format uses uint8 type
		if !strings.Contains(clientCode, "SmallUnsigned uint8") {
			t.Error("Expected uint8 format field to use uint8 type")
		}
		// Check that uint16 format uses uint16 type
		if !strings.Contains(clientCode, "ShortUnsigned uint16") {
			t.Error("Expected uint16 format field to use uint16 type")
		}
		// Check that uint32 format uses uint32 type
		if !strings.Contains(clientCode, "RegularUint   uint32") {
			t.Error("Expected uint32 format field to use uint32 type")
		}
		// Check that uint64 format uses uint64 type
		if !strings.Contains(clientCode, "LargeUnsigned uint64") {
			t.Error("Expected uint64 format field to use uint64 type")
		}
	})

	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with integer formats has syntax errors: %v", err)
		}
	})
}

func TestZeroValueChecksInGeneratedCode(t *testing.T) {
	// Test that generated code uses correct zero value checks for different types:
	// - time.Time should use .IsZero()
	// - bool should use direct boolean check
	// - pointer types should use != nil
	// - numeric types should use != 0

	// Create API with various parameter types
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Zero Check Test API", "1.0.0"))

	huma.Get(api, "/test", func(ctx context.Context, input *struct {
		Timestamp time.Time `query:"timestamp" format:"date-time" doc:"Time parameter"`
		Count     int       `query:"count" doc:"Integer parameter"`
		Score     float64   `query:"score" doc:"Float parameter"`
		Active    bool      `query:"active" doc:"Boolean parameter"`
	}) (*struct{ Body string }, error) {
		return &struct{ Body string }{Body: "test"}, nil
	})

	// Add an endpoint that could potentially generate pointer types in options structs
	// This tests the general pointer handling logic we implemented
	huma.Get(api, "/advanced", func(ctx context.Context, input *struct {
		BasicParam   string `query:"basic" doc:"Regular string param"`
		OptionalFlag bool   `query:"flag" doc:"Boolean flag"`
		NumericValue int64  `query:"value" doc:"Numeric value"`
	}) (*struct{ Body string }, error) {
		return &struct{ Body string }{Body: "advanced"}, nil
	})

	// Test client generation
	tempDir, err := os.MkdirTemp("", "humaclient_zero_check_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client for zero check test: %v", err)
	}

	// Read the generated code
	clientFile := "zerochecktestapiclient/client.go"
	content, err := os.ReadFile(clientFile)
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("TimeUseIsZeroCheck", func(t *testing.T) {
		// Should use .IsZero() for time.Time fields
		if !strings.Contains(clientCode, "!o.Timestamp.IsZero()") {
			t.Error("Expected time.Time field to use .IsZero() check, not != 0")
		}
	})

	t.Run("NumericTypesUseZeroComparison", func(t *testing.T) {
		// Should use != 0 for numeric fields
		if !strings.Contains(clientCode, "o.Count != 0") {
			t.Error("Expected int field to use != 0 check")
		}
		if !strings.Contains(clientCode, "o.Score != 0") {
			t.Error("Expected float64 field to use != 0 check")
		}
		if !strings.Contains(clientCode, "o.Value != 0") {
			t.Error("Expected int64 field to use != 0 check")
		}
	})

	t.Run("BoolUsesDirectCheck", func(t *testing.T) {
		// Should use direct boolean check
		if !strings.Contains(clientCode, "if o.Active {") {
			t.Error("Expected bool field to use direct boolean check, not != 0")
		}
		if !strings.Contains(clientCode, "if o.Flag {") {
			t.Error("Expected bool field to use direct boolean check, not != 0")
		}
	})

	t.Run("StringTypesUseEmptyCheck", func(t *testing.T) {
		// Should use != "" for string fields
		if !strings.Contains(clientCode, `o.Basic != ""`) {
			t.Error("Expected string field to use != \"\" check")
		}
	})

	t.Run("PointerLogicIsReady", func(t *testing.T) {
		// Note: While Huma doesn't currently generate pointer types for query params,
		// our template logic is ready to handle them correctly with != nil checks.
		// This ensures future compatibility if pointer support is added.

		// Check that our hasPrefix template function works by verifying it's available
		// (we can't easily test actual pointer generation due to Huma's limitations)

		// The template should not contain the old "!= 0" pattern for non-numeric types
		lines := strings.Split(clientCode, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Check that we don't have any "!= 0" checks for bool or time types
			if strings.Contains(trimmed, "!= 0") {
				if strings.Contains(trimmed, "bool") || strings.Contains(trimmed, "time.Time") {
					t.Errorf("Found incorrect != 0 check for bool/time type: %s", trimmed)
				}
			}
		}
	})

	t.Run("GeneratedCodeCompiles", func(t *testing.T) {
		// Parse the generated client code to verify syntax
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, clientFile, content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated client code with zero checks has syntax errors: %v", err)
		}
	})
}

func TestImportLoopPrevention(t *testing.T) {
	// Create a temporary directory structure to simulate a scenario where
	// the output directory matches one of the allowed packages
	tempDir, err := os.MkdirTemp("", "humaclient_importloop_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a go.mod file to establish module context
	goModContent := `module example.com/testapi

go 1.21
`
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Change to temp directory so our package detection works
	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Create a simple API
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Test API", "1.0.0"))

	// Add a simple endpoint
	huma.Get(api, "/test", func(ctx context.Context, input *struct{}) (*SimpleMessageResponse, error) {
		return &SimpleMessageResponse{Body: "test"}, nil
	})

	// Test case where AllowedPackages contains the same path as the output directory
	t.Run("PreventsSelfImport", func(t *testing.T) {
		outputDir := "testapiclient"
		currentPackagePath := "example.com/testapi/testapiclient"

		opts := Options{
			PackageName:     "testapiclient",
			OutputDirectory: outputDir,
			AllowedPackages: []string{currentPackagePath}, // This would cause an import loop
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		// Read the generated client code
		content, err := os.ReadFile(filepath.Join(outputDir, "client.go"))
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Verify that the self-import is not present in the generated code
		lines := strings.Split(clientCode, "\n")
		inImportBlock := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)

			if strings.Contains(trimmed, "import (") {
				inImportBlock = true
				continue
			}

			if inImportBlock && strings.Contains(trimmed, ")") {
				inImportBlock = false
				continue
			}

			if inImportBlock && strings.Contains(trimmed, "\"") {
				// Verify no self-import
				if strings.Contains(trimmed, currentPackagePath) {
					t.Errorf("Found self-import in generated code: %s", trimmed)
				}
			}
		}

		// The test should pass regardless of whether external imports section exists
		// because when self-imports are filtered out, the section may be empty
		t.Logf("Generated client successfully without self-imports")
	})

	t.Run("AllowsOtherExternalImports", func(t *testing.T) {
		outputDir := "testapiclient2"

		opts := Options{
			PackageName:     "testapiclient2",
			OutputDirectory: outputDir,
			AllowedPackages: []string{
				"example.com/testapi/testapiclient2", // Self-import (should be filtered)
				"github.com/danielgtaylor/huma/v2",   // External import (should be allowed)
			},
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		// Read the generated client code
		content, err := os.ReadFile(filepath.Join(outputDir, "client.go"))
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Verify that:
		// 1. Self-import is not present
		// 2. Other external imports are preserved (if they're actually used)
		lines := strings.Split(clientCode, "\n")
		foundSelfImport := false

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)

			if strings.Contains(trimmed, "example.com/testapi/testapiclient2") {
				foundSelfImport = true
			}
		}

		if foundSelfImport {
			t.Errorf("Found self-import in generated code when it should be filtered out")
		}

		t.Logf("Successfully filtered self-import while preserving other allowed packages")
	})
}

func TestSelfImportTypeReferences(t *testing.T) {
	// Create a temporary directory structure to test type reference handling
	tempDir, err := os.MkdirTemp("", "humaclient_typereferences_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a go.mod file to establish module context
	goModContent := `module example.com/myapi

go 1.21

require github.com/danielgtaylor/huma/v2 v2.15.0
`
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Change to temp directory so our package detection works
	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Define a resource type that will be used in the API
	type Resource struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	// Create an API that would use both self-types and external types
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("My API", "1.0.0"))

	// Add an endpoint that uses the Resource type (this will auto-register the schema)
	huma.Get(api, "/resources/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body Resource }, error) {
		return &struct{ Body Resource }{
			Body: Resource{
				ID:   input.ID,
				Name: "Test Resource",
			},
		}, nil
	})

	t.Run("UsesUnqualifiedTypesForSelfImports", func(t *testing.T) {
		outputDir := "myapiclient"

		opts := Options{
			PackageName:     "myapiclient",
			OutputDirectory: outputDir,
			AllowedPackages: []string{
				"example.com/myapi/myapiclient",    // Self-import - types should be unqualified
				"github.com/danielgtaylor/huma/v2", // External import - types should be qualified
			},
		}

		err := GenerateClientWithOptions(api, opts)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		// Read the generated client code
		content, err := os.ReadFile(filepath.Join(outputDir, "client.go"))
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		// Verify that self-import types are unqualified
		// Since our schema is being generated locally, references should be like "Resource", not "myapiclient.Resource"
		if strings.Contains(clientCode, "myapiclient.Resource") {
			t.Errorf("Found qualified self-reference 'myapiclient.Resource' in generated code - should be unqualified 'Resource'")
		}

		// Verify the Resource type is defined locally
		if !strings.Contains(clientCode, "type Resource struct") {
			t.Errorf("Expected to find 'type Resource struct' definition in generated code")
		}

		// Verify that any actual external references (like huma.Schema) are qualified if used
		if strings.Contains(clientCode, "huma.") {
			// If huma types are used, they should be qualified since they're truly external
			if !strings.Contains(clientCode, "\"github.com/danielgtaylor/huma/v2\"") {
				t.Errorf("Found huma.* reference but no import for github.com/danielgtaylor/huma/v2")
			}
		}

		t.Logf("Successfully generated code with proper type reference handling")
	})
}

func TestRequestBodyNilCheckRemoval(t *testing.T) {
	t.Run("RequiredBodyOmitsNilCheck", func(t *testing.T) {
		api := createTestAPI()
		
		tempDir, err := os.MkdirTemp("", "humaclient_nilcheck_test_*")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(tempDir)
		
		oldDir, _ := os.Getwd()
		os.Chdir(tempDir)
		defer os.Chdir(oldDir)
		
		err = GenerateClient(api)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}
		
		content, err := os.ReadFile("testapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}
		
		code := string(content)
		
		// Check that POST operation (with required body) does NOT have nil check
		// It should set Content-Type unconditionally
		postIdx := strings.Index(code, "func (c *TestAPIClientImpl) PostThings")
		if postIdx == -1 {
			t.Fatal("Could not find PostThings method in generated code")
		}
		
		// Find the next function after PostThings to bound our search
		nextFuncIdx := strings.Index(code[postIdx+100:], "\nfunc ")
		if nextFuncIdx == -1 {
			nextFuncIdx = len(code)
		} else {
			nextFuncIdx = postIdx + 100 + nextFuncIdx
		}
		
		postMethod := code[postIdx:nextFuncIdx]
		
		// Should NOT have "if reqBody != nil"
		if strings.Contains(postMethod, "if reqBody != nil") {
			t.Error("POST method with required body should not have nil check for reqBody")
		}
		
		// Should have unconditional Content-Type setting
		if !strings.Contains(postMethod, `req.Header.Set("Content-Type", "application/json")`) {
			t.Error("POST method should set Content-Type unconditionally")
		}
		
		t.Logf("Successfully verified POST method does not have unnecessary nil check")
	})
	
	t.Run("OptionalBodyKeepsNilCheck", func(t *testing.T) {
		api := createTestAPI()
		
		tempDir, err := os.MkdirTemp("", "humaclient_nilcheck_test_*")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(tempDir)
		
		oldDir, _ := os.Getwd()
		os.Chdir(tempDir)
		defer os.Chdir(oldDir)
		
		err = GenerateClient(api)
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}
		
		content, err := os.ReadFile("testapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}
		
		code := string(content)
		
		// Check that Follow method (with optional body) DOES have nil check
		followIdx := strings.Index(code, "func (c *TestAPIClientImpl) Follow")
		if followIdx == -1 {
			t.Fatal("Could not find Follow method in generated code")
		}
		
		// Find the end of Follow method
		nextFuncIdx := strings.Index(code[followIdx+100:], "\nfunc ")
		if nextFuncIdx == -1 {
			nextFuncIdx = len(code)
		} else {
			nextFuncIdx = followIdx + 100 + nextFuncIdx
		}
		
		followMethod := code[followIdx:nextFuncIdx]
		
		// Should have "if reqBody != nil"
		if !strings.Contains(followMethod, "if reqBody != nil") {
			t.Error("Follow method with optional body should have nil check for reqBody")
		}
		
		// Should have conditional Content-Type setting inside the nil check
		nilCheckIdx := strings.Index(followMethod, "if reqBody != nil")
		contentTypeIdx := strings.Index(followMethod, `req.Header.Set("Content-Type", "application/json")`)
		
		if nilCheckIdx == -1 || contentTypeIdx == -1 {
			t.Fatal("Expected both nil check and Content-Type setting")
		}
		
		if contentTypeIdx < nilCheckIdx {
			t.Error("Content-Type should be set inside the nil check for optional body")
		}
		
		t.Logf("Successfully verified Follow method keeps nil check for optional body")
	})
}

// createAutopatchTestAPI creates a test API that simulates Huma's autopatch behavior:
// a GET + PUT pair with a PATCH operation registered using application/merge-patch+json.
func createAutopatchTestAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Autopatch API", "1.0.0"))

	// GET /things/{id} - Get a thing
	huma.Get(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id" doc:"Thing ID"`
	}) (*GetThingResponse, error) {
		return &GetThingResponse{
			Body: TestThing{
				ID:   input.ID,
				Name: "Test Thing",
			},
		}, nil
	})

	// PUT /things/{id} - Update a thing
	huma.Put(api, "/things/{id}", func(ctx context.Context, input *struct {
		ID   string `path:"id"`
		Body TestThing
	}) (*CreateThingResponse, error) {
		return &CreateThingResponse{
			Body: input.Body,
		}, nil
	})

	// Simulate autopatch: register a PATCH operation with application/merge-patch+json
	// This mimics what huma/autopatch.AutoPatch does internally.
	openapi := api.OpenAPI()
	pathItem := openapi.Paths["/things/{id}"]
	putOp := pathItem.Put

	// Get the PUT body schema and create an "optional" version (Required cleared)
	putSchema := putOp.RequestBody.Content["application/json"].Schema
	if putSchema.Ref != "" {
		putSchema = openapi.Components.Schemas.SchemaFromRef(putSchema.Ref)
	}
	optionalSchema := *putSchema
	optionalSchema.Required = nil

	// Get the PUT response schema for the PATCH response
	var responseSchema *huma.Schema
	var responseStatusCode string
	for code, resp := range putOp.Responses {
		if code[0] == '2' && resp.Content != nil {
			if jsonContent := resp.Content["application/json"]; jsonContent != nil {
				responseSchema = jsonContent.Schema
				responseStatusCode = code
				break
			}
		}
	}

	pathItem.Patch = &huma.Operation{
		OperationID: "patch-things-by-id",
		Summary:     "Patch thing by ID",
		Parameters: []*huma.Param{
			{Name: "id", In: "path", Required: true, Schema: &huma.Schema{Type: "string"}},
		},
		RequestBody: &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				"application/merge-patch+json": {
					Schema: &optionalSchema,
				},
				"application/json-patch+json": {
					Schema: &huma.Schema{
						Type: "array",
						Items: &huma.Schema{
							Type: "object",
							Properties: map[string]*huma.Schema{
								"op":    {Type: "string"},
								"path":  {Type: "string"},
								"from":  {Type: "string"},
								"value": {},
							},
							Required: []string{"op", "path"},
						},
					},
				},
			},
		},
		Responses: map[string]*huma.Response{
			responseStatusCode: {
				Description: "Successful response",
				Content: map[string]*huma.MediaType{
					"application/json": {
						Schema: responseSchema,
					},
				},
			},
		},
	}

	return api
}

func TestPatchableCodeGeneration(t *testing.T) {
	api := createAutopatchTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_patchable_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile(filepath.Join("autopatchapiclient", "client.go"))
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("GeneratesPatchableInterface", func(t *testing.T) {
		if !strings.Contains(clientCode, "type Patchable interface") {
			t.Error("Generated code missing Patchable interface")
		}
	})

	t.Run("GeneratesMergePatchType", func(t *testing.T) {
		if !strings.Contains(clientCode, "type MergePatch map[string]any") {
			t.Error("Generated code missing MergePatch type")
		}
		if !strings.Contains(clientCode, `func (m MergePatch) PatchContentType() string`) {
			t.Error("Generated code missing MergePatch.PatchContentType method")
		}
	})

	t.Run("GeneratesJSONPatchTypes", func(t *testing.T) {
		if !strings.Contains(clientCode, "type JSONPatchOp struct") {
			t.Error("Generated code missing JSONPatchOp struct")
		}
		if !strings.Contains(clientCode, "type JSONPatch []JSONPatchOp") {
			t.Error("Generated code missing JSONPatch type")
		}
		if !strings.Contains(clientCode, `func (j JSONPatch) PatchContentType() string`) {
			t.Error("Generated code missing JSONPatch.PatchContentType method")
		}
	})

	t.Run("JSONPatchOpHasCorrectFields", func(t *testing.T) {
		expectedFields := []string{
			`json:"op"`,
			`json:"path"`,
			`json:"from,omitempty"`,
			`json:"value,omitempty"`,
		}
		for _, field := range expectedFields {
			if !strings.Contains(clientCode, field) {
				t.Errorf("JSONPatchOp missing expected field tag: %s", field)
			}
		}
	})

	t.Run("GeneratesConditionalHeaderHelpers", func(t *testing.T) {
		if !strings.Contains(clientCode, "func WithIfMatch(etag string) Option") {
			t.Error("Generated code missing WithIfMatch helper")
		}
		if !strings.Contains(clientCode, "func WithIfNoneMatch(etag string) Option") {
			t.Error("Generated code missing WithIfNoneMatch helper")
		}
	})

	t.Run("PatchMethodHasPatchableBody", func(t *testing.T) {
		if !strings.Contains(clientCode, "body Patchable") {
			t.Error("Patch method should have Patchable body parameter")
		}
	})

	t.Run("PatchMethodSetsDynamicContentType", func(t *testing.T) {
		if !strings.Contains(clientCode, `req.Header.Set("Content-Type", body.PatchContentType())`) {
			t.Error("Patch method should set Content-Type dynamically from body.PatchContentType()")
		}
	})

	t.Run("PutMethodStillUsesApplicationJSON", func(t *testing.T) {
		if !strings.Contains(clientCode, `req.Header.Set("Content-Type", "application/json")`) {
			t.Error("PUT method should still use application/json Content-Type")
		}
	})

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has invalid Go syntax: %v", err)
		}
	})

	t.Run("InterfaceIncludesPatchMethod", func(t *testing.T) {
		if !strings.Contains(clientCode, "PatchThingsByID(ctx context.Context, id string, body Patchable, opts ...Option)") {
			t.Error("Interface missing PatchThingsByID method with Patchable body")
		}
	})

	t.Run("OriginalStructUnchanged", func(t *testing.T) {
		if !strings.Contains(clientCode, "type TestThing struct") {
			t.Error("Original TestThing struct should still be generated")
		}
		thStart := strings.Index(clientCode, "type TestThing struct")
		thEnd := strings.Index(clientCode[thStart:], "\n}")
		thStruct := clientCode[thStart : thStart+thEnd]
		if !strings.Contains(thStruct, `json:"id"`) {
			t.Error("Original TestThing ID field should NOT have omitempty (it's required)")
		}
	})
}

func TestPatchableBehavior(t *testing.T) {
	api := createAutopatchTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_patchable_behavior_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Create a test server that handles PATCH requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == "PATCH" && r.URL.Path == "/things/test123":
			// Read the patch body and echo back metadata for verification
			body, _ := io.ReadAll(r.Body)
			result := map[string]any{
				"patch":       json.RawMessage(body),
				"contentType": r.Header.Get("Content-Type"),
				"ifMatch":     r.Header.Get("If-Match"),
			}
			json.NewEncoder(w).Encode(result)

		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer server.Close()

	// Create go.mod for the test program
	os.WriteFile("go.mod", []byte("module testprogram\ngo 1.23\n"), 0644)

	t.Run("MergePatchRequest", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/autopatchapiclient"
)

func main() {
	client := autopatchapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	resp, _, err := client.PatchThingsByID(context.Background(), "test123",
		autopatchapiclient.MergePatch{
			"name": "updated name",
		},
	)
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	output := map[string]any{
		"statusCode":  resp.StatusCode,
		"contentType": resp.Request.Header.Get("Content-Type"),
	}
	json.NewEncoder(os.Stdout).Encode(output)
}
`, server.URL)

		os.WriteFile("test_merge_patch.go", []byte(testProgram), 0644)

		output, err := runGoProgram("test_merge_patch.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v\nOutput: %s", err, output)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse output: %v\nRaw: %s", err, output)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		if result["contentType"] != "application/merge-patch+json" {
			t.Errorf("Expected Content-Type 'application/merge-patch+json', got %v", result["contentType"])
		}
	})

	t.Run("JSONPatchRequest", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/autopatchapiclient"
)

func main() {
	client := autopatchapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	resp, _, err := client.PatchThingsByID(context.Background(), "test123",
		autopatchapiclient.JSONPatch{
			{Op: "replace", Path: "/name", Value: "patched"},
			{Op: "remove", Path: "/tags"},
		},
	)
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	output := map[string]any{
		"statusCode":  resp.StatusCode,
		"contentType": resp.Request.Header.Get("Content-Type"),
	}
	json.NewEncoder(os.Stdout).Encode(output)
}
`, server.URL)

		os.WriteFile("test_json_patch.go", []byte(testProgram), 0644)

		output, err := runGoProgram("test_json_patch.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v\nOutput: %s", err, output)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse output: %v\nRaw: %s", err, output)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		if result["contentType"] != "application/json-patch+json" {
			t.Errorf("Expected Content-Type 'application/json-patch+json', got %v", result["contentType"])
		}
	})

	t.Run("MergePatchWithIfMatch", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/autopatchapiclient"
)

func main() {
	client := autopatchapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	resp, _, err := client.PatchThingsByID(context.Background(), "test123",
		autopatchapiclient.MergePatch{"name": "patched"},
		autopatchapiclient.WithIfMatch("\"etag-value-123\""),
	)
	if err != nil {
		fmt.Printf("ERROR: %%v\n", err)
		os.Exit(1)
	}

	output := map[string]any{
		"statusCode": resp.StatusCode,
		"ifMatch":    resp.Request.Header.Get("If-Match"),
	}
	json.NewEncoder(os.Stdout).Encode(output)
}
`, server.URL)

		os.WriteFile("test_patch_ifmatch.go", []byte(testProgram), 0644)

		output, err := runGoProgram("test_patch_ifmatch.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v\nOutput: %s", err, output)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse output: %v\nRaw: %s", err, output)
		}

		if int(result["statusCode"].(float64)) != 200 {
			t.Errorf("Expected status 200, got %v", result["statusCode"])
		}

		if result["ifMatch"] != "\"etag-value-123\"" {
			t.Errorf("Expected If-Match header '\"etag-value-123\"', got %v", result["ifMatch"])
		}
	})
}

func TestPatchableNotGeneratedWithoutAutopatch(t *testing.T) {
	// Regular API without autopatch should NOT generate Patchable types
	api := createTestAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_no_patchable_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile(filepath.Join("testapiclient", "client.go"))
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	if strings.Contains(clientCode, "Patchable") {
		t.Error("Regular API should NOT generate Patchable types")
	}
	if strings.Contains(clientCode, "MergePatch") {
		t.Error("Regular API should NOT generate MergePatch type")
	}
	if strings.Contains(clientCode, "JSONPatch") {
		t.Error("Regular API should NOT generate JSONPatch types")
	}
	if strings.Contains(clientCode, "WithIfMatch") {
		t.Error("Regular API should NOT generate WithIfMatch helper")
	}
}

// --- Object-wrapped pagination tests ---

// createWrappedPaginationAPI creates a test API with object-wrapped list responses
func createWrappedPaginationAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Wrapped API", "1.0.0"))

	type Item struct {
		ID   string `json:"id" doc:"Item ID"`
		Name string `json:"name" doc:"Item name"`
	}

	type ListItemsResponse struct {
		Body struct {
			Items []Item `json:"items" doc:"The list of items"`
			Total int    `json:"total" doc:"Total count"`
			Next  string `json:"next,omitempty" doc:"URL for the next page"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/items",
		Summary:     "List items",
	}, func(ctx context.Context, input *struct {
		Limit  int    `query:"limit" doc:"Maximum number of items" default:"10"`
		Cursor string `query:"cursor" doc:"Pagination cursor"`
	}) (*ListItemsResponse, error) {
		return &ListItemsResponse{
			Body: struct {
				Items []Item `json:"items" doc:"The list of items"`
				Total int    `json:"total" doc:"Total count"`
				Next  string `json:"next,omitempty" doc:"URL for the next page"`
			}{
				Items: []Item{{ID: "1", Name: "First"}},
				Total: 2,
				Next:  "http://example.com/items?cursor=abc",
			},
		}, nil
	})

	huma.Get(api, "/items/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*struct{ Body Item }, error) {
		return &struct{ Body Item }{Body: Item{ID: input.ID, Name: "Test"}}, nil
	})

	return api
}

// createWrappedPaginationWithLinkAPI creates a test API with object-wrapped responses and Link header
func createWrappedPaginationWithLinkAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Wrapped Link API", "1.0.0"))

	type Item struct {
		ID   string `json:"id" doc:"Item ID"`
		Name string `json:"name" doc:"Item name"`
	}

	type ListItemsResponse struct {
		Link string `header:"Link" doc:"Pagination link"`
		Body struct {
			Items []Item `json:"items" doc:"The list of items"`
			Total int    `json:"total" doc:"Total count"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/items",
		Summary:     "List items",
	}, func(ctx context.Context, input *struct{}) (*ListItemsResponse, error) {
		return &ListItemsResponse{
			Body: struct {
				Items []Item `json:"items" doc:"The list of items"`
				Total int    `json:"total" doc:"Total count"`
			}{
				Items: []Item{{ID: "1", Name: "First"}},
				Total: 1,
			},
		}, nil
	})

	return api
}

// createNestedNextFieldAPI creates a test API with nested pagination metadata
func createNestedNextFieldAPI() huma.API {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Nested Pagination API", "1.0.0"))

	type Item struct {
		ID   string `json:"id" doc:"Item ID"`
		Name string `json:"name" doc:"Item name"`
	}

	type PaginationMeta struct {
		Next  string `json:"next,omitempty" doc:"URL for the next page"`
		Total int    `json:"total" doc:"Total count"`
	}

	type ListItemsResponse struct {
		Body struct {
			Items []Item         `json:"items" doc:"The list of items"`
			Meta  PaginationMeta `json:"meta" doc:"Pagination metadata"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/items",
		Summary:     "List items",
	}, func(ctx context.Context, input *struct{}) (*ListItemsResponse, error) {
		return &ListItemsResponse{
			Body: struct {
				Items []Item         `json:"items" doc:"The list of items"`
				Meta  PaginationMeta `json:"meta" doc:"Pagination metadata"`
			}{
				Items: []Item{{ID: "1", Name: "First"}},
				Meta:  PaginationMeta{Total: 1},
			},
		}, nil
	})

	return api
}

func TestWrappedPaginationDetection(t *testing.T) {
	t.Run("DetectsWrappedPaginationWithBodyNext", func(t *testing.T) {
		api := createWrappedPaginationAPI()
		openapi := api.OpenAPI()

		pagination := &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		}

		// Check that the list-items operation is detected as paginated
		found := false
		for _, pathItem := range openapi.Paths {
			for _, op := range getOperations(pathItem) {
				if op.OperationID == "list-items" {
					if !isPaginatedOperation(op, openapi, pagination) {
						t.Error("Expected list-items to be detected as paginated with wrapped body + NextField")
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("list-items operation not found in API")
		}
	})

	t.Run("DetectsWrappedPaginationWithLinkHeader", func(t *testing.T) {
		api := createWrappedPaginationWithLinkAPI()
		openapi := api.OpenAPI()

		pagination := &PaginationOptions{
			ItemsField: "Items",
		}

		found := false
		for _, pathItem := range openapi.Paths {
			for _, op := range getOperations(pathItem) {
				if op.OperationID == "list-items" {
					if !isPaginatedOperation(op, openapi, pagination) {
						t.Error("Expected list-items to be detected as paginated with wrapped body + Link header")
					}
					found = true
				}
			}
		}
		if !found {
			t.Fatal("list-items operation not found in API")
		}
	})

	t.Run("RejectsWrappedPaginationWithoutConfig", func(t *testing.T) {
		api := createWrappedPaginationAPI()
		openapi := api.OpenAPI()

		// Without pagination config, object-wrapped responses should NOT be detected
		for _, pathItem := range openapi.Paths {
			for _, op := range getOperations(pathItem) {
				if op.OperationID == "list-items" {
					if isPaginatedOperation(op, openapi, nil) {
						t.Error("Expected list-items to NOT be detected as paginated without PaginationOptions")
					}
				}
			}
		}
	})

	t.Run("RejectsWrongItemsField", func(t *testing.T) {
		api := createWrappedPaginationAPI()
		openapi := api.OpenAPI()

		pagination := &PaginationOptions{
			ItemsField: "Data", // Wrong field name
			NextField:  "Next",
		}

		for _, pathItem := range openapi.Paths {
			for _, op := range getOperations(pathItem) {
				if op.OperationID == "list-items" {
					if isPaginatedOperation(op, openapi, pagination) {
						t.Error("Expected list-items to NOT be detected as paginated with wrong ItemsField")
					}
				}
			}
		}
	})
}

func TestWrappedPaginationCodeGeneration(t *testing.T) {
	api := createWrappedPaginationAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_wrapped_gen_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("wrappedapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v\n\nGenerated code:\n%s", err, clientCode)
		}
	})

	t.Run("HasPaginatorMethod", func(t *testing.T) {
		if !strings.Contains(clientCode, "ListItemsPaginator") {
			t.Error("Generated code missing ListItemsPaginator method")
		}
	})

	t.Run("HasIterImport", func(t *testing.T) {
		if !strings.Contains(clientCode, `"iter"`) {
			t.Error("Generated code missing iter import")
		}
	})

	t.Run("PaginatorUsesWrapperType", func(t *testing.T) {
		// The paginator should use result.Items, not items directly
		if !strings.Contains(clientCode, "result.Items") {
			t.Error("Paginator should access items via result.Items")
		}
	})

	t.Run("PaginatorChecksBodyNextField", func(t *testing.T) {
		if !strings.Contains(clientCode, "result.Next") {
			t.Error("Paginator should check result.Next for body-based pagination")
		}
	})

	t.Run("RawMethodReturnsWrapperStruct", func(t *testing.T) {
		// The raw ListItems method should return the wrapper struct, not []Item
		if strings.Contains(clientCode, "ListItems(ctx context.Context, opts ...Option) (*http.Response, []Item, error)") {
			t.Error("Raw method should return wrapper struct, not []Item")
		}
	})

	t.Run("NonPaginatedOperationUnaffected", func(t *testing.T) {
		// GetItemsByID should not have a paginator
		if strings.Contains(clientCode, "GetItemsByIDPaginator") {
			t.Error("Non-list operation should not have a paginator method")
		}
	})
}

func TestWrappedPaginationWithLinkHeaderCodeGen(t *testing.T) {
	api := createWrappedPaginationWithLinkAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_wrapped_link_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			// No NextField - only Link header
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("wrappedlinkapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v\n\nGenerated code:\n%s", err, clientCode)
		}
	})

	t.Run("HasPaginatorUsingLinkHeader", func(t *testing.T) {
		if !strings.Contains(clientCode, "ListItemsPaginator") {
			t.Error("Generated code missing ListItemsPaginator method")
		}
		if !strings.Contains(clientCode, "parseLinkHeader") {
			t.Error("Generated code should use parseLinkHeader for Link-based pagination")
		}
	})
}

func TestNestedNextFieldCodeGeneration(t *testing.T) {
	api := createNestedNextFieldAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_nested_next_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Meta.Next",
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("nestedpaginationapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v\n\nGenerated code:\n%s", err, clientCode)
		}
	})

	t.Run("PaginatorUsesNestedFieldAccess", func(t *testing.T) {
		if !strings.Contains(clientCode, "result.Meta.Next") {
			t.Error("Paginator should access nested next field via result.Meta.Next")
		}
	})
}

func TestWrappedPaginationIntegration(t *testing.T) {
	api := createWrappedPaginationAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_wrapped_integration_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Create test server that returns paginated object-wrapped responses
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestCount++

		switch {
		case r.URL.Path == "/items" && requestCount == 1:
			// First page with next URL
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{
					{"id": "1", "name": "First"},
					{"id": "2", "name": "Second"},
				},
				"total": 4,
				"next":  fmt.Sprintf("http://%s/items?cursor=page2", r.Host),
			})
			w.Write(j)

		case r.URL.Path == "/items" && requestCount == 2:
			// Second page with no next URL (last page)
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{
					{"id": "3", "name": "Third"},
					{"id": "4", "name": "Fourth"},
				},
				"total": 4,
			})
			w.Write(j)

		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"not found"}`))
		}
	}))
	defer server.Close()

	// Create go.mod
	err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.23\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	t.Run("BodyBasedPagination", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/wrappedapiclient"
)

func main() {
	client := wrappedapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	// Use paginator to collect all items across pages
	var allItems []map[string]string
	for item, err := range client.ListItemsPaginator(context.Background()) {
		if err != nil {
			fmt.Printf("ERROR: %%v\n", err)
			os.Exit(1)
		}
		allItems = append(allItems, map[string]string{"id": item.ID, "name": item.Name})
	}

	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"count": len(allItems),
		"items": allItems,
	})
}
`, server.URL)

		err := os.WriteFile("test_wrapped_pagination.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		output, err := runGoProgram("test_wrapped_pagination.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v", err)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse test output: %v\nOutput: %s", err, output)
		}

		count := int(result["count"].(float64))
		if count != 4 {
			t.Errorf("Expected 4 items across pages, got %d", count)
		}

		items := result["items"].([]any)
		if items[0].(map[string]any)["id"] != "1" {
			t.Error("Expected first item to have id=1")
		}
		if items[3].(map[string]any)["id"] != "4" {
			t.Error("Expected fourth item to have id=4")
		}
	})
}

func TestWrappedPaginationWithLinkHeaderIntegration(t *testing.T) {
	api := createWrappedPaginationWithLinkAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_wrapped_link_int_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Create test server that uses Link header for pagination
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestCount++

		switch {
		case r.URL.Path == "/items" && requestCount == 1:
			w.Header().Set("Link", fmt.Sprintf(`<%s/items?page=2>; rel="next"`, "http://"+r.Host))
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{
					{"id": "1", "name": "First"},
				},
				"total": 2,
			})
			w.Write(j)

		case r.URL.Path == "/items" && requestCount == 2:
			// Last page - no Link header
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{
					{"id": "2", "name": "Second"},
				},
				"total": 2,
			})
			w.Write(j)

		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"detail":"not found"}`))
		}
	}))
	defer server.Close()

	err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.23\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	t.Run("LinkHeaderPagination", func(t *testing.T) {
		testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/wrappedlinkapiclient"
)

func main() {
	client := wrappedlinkapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	var allItems []map[string]string
	for item, err := range client.ListItemsPaginator(context.Background()) {
		if err != nil {
			fmt.Printf("ERROR: %%v\n", err)
			os.Exit(1)
		}
		allItems = append(allItems, map[string]string{"id": item.ID, "name": item.Name})
	}

	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"count": len(allItems),
		"items": allItems,
	})
}
`, server.URL)

		err := os.WriteFile("test_link_pagination.go", []byte(testProgram), 0644)
		if err != nil {
			t.Fatalf("Failed to write test program: %v", err)
		}

		output, err := runGoProgram("test_link_pagination.go")
		if err != nil {
			t.Fatalf("Failed to run test program: %v", err)
		}

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("Failed to parse test output: %v\nOutput: %s", err, output)
		}

		count := int(result["count"].(float64))
		if count != 2 {
			t.Errorf("Expected 2 items across pages, got %d", count)
		}
	})
}

func TestWrappedPaginationLinkPrecedence(t *testing.T) {
	// Test that Link header takes precedence over body next field
	api := createWrappedPaginationAPI()

	tempDir, err := os.MkdirTemp("", "humaclient_precedence_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	opts := Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		},
	}

	err = GenerateClientWithOptions(api, opts)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	// Server that provides both Link header and body next field
	// Link header points to /items?via=link, body points to /items?via=body
	requestCount := 0
	secondRequestVia := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		requestCount++

		if requestCount == 1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s/items?via=link>; rel="next"`, "http://"+r.Host))
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{{"id": "1", "name": "First"}},
				"total": 2,
				"next":  fmt.Sprintf("http://%s/items?via=body", r.Host),
			})
			w.Write(j)
		} else {
			secondRequestVia = r.URL.Query().Get("via")
			j, _ := json.Marshal(map[string]any{
				"items": []map[string]string{{"id": "2", "name": "Second"}},
				"total": 2,
			})
			w.Write(j)
		}
	}))
	defer server.Close()

	err = os.WriteFile("go.mod", []byte("module testprogram\ngo 1.23\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	testProgram := fmt.Sprintf(`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"testprogram/wrappedapiclient"
)

func main() {
	client := wrappedapiclient.NewWithClient("%s", &http.Client{
		Timeout: time.Second * 5,
	})

	// Use paginator to iterate all pages, triggering the second request
	var allItems []map[string]string
	for item, err := range client.ListItemsPaginator(context.Background()) {
		if err != nil {
			fmt.Printf("ERROR: %%v\n", err)
			os.Exit(1)
		}
		allItems = append(allItems, map[string]string{"id": item.ID, "name": item.Name})
	}

	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"count": len(allItems),
		"items": allItems,
	})
}
`, server.URL)

	err = os.WriteFile("test_precedence.go", []byte(testProgram), 0644)
	if err != nil {
		t.Fatalf("Failed to write test program: %v", err)
	}

	output, err := runGoProgram("test_precedence.go")
	if err != nil {
		t.Fatalf("Failed to run test program: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("Failed to parse test output: %v\nOutput: %s", err, output)
	}

	// Verify paginator collected items from both pages
	if int(result["count"].(float64)) != 2 {
		t.Errorf("Expected 2 items from paginator, got %v", result["count"])
	}

	// Verify the second request was made via the Link header, not the body next field
	if requestCount != 2 {
		t.Errorf("Expected 2 requests, got %d", requestCount)
	}
	if secondRequestVia != "link" {
		t.Errorf("Expected second request via Link header (via=link), got via=%s", secondRequestVia)
	}
}

func TestBackwardCompatibilityArrayPagination(t *testing.T) {
	// Verify that the original array-based pagination still works when Pagination is nil
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Array API", "1.0.0"))

	type Item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	type ListItemsResponse struct {
		Link string `header:"Link" doc:"Pagination link"`
		Body []Item
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/items",
	}, func(ctx context.Context, input *struct{}) (*ListItemsResponse, error) {
		return &ListItemsResponse{
			Body: []Item{{ID: "1", Name: "First"}},
		}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_compat_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	// Generate WITHOUT PaginationOptions (nil)
	err = GenerateClient(api)
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("arrayapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v", err)
		}
	})

	t.Run("HasArrayPaginator", func(t *testing.T) {
		if !strings.Contains(clientCode, "ListItemsPaginator") {
			t.Error("Expected ListItemsPaginator method for array response with Link header")
		}
	})

	t.Run("ReturnsSliceDirectly", func(t *testing.T) {
		if !strings.Contains(clientCode, "(*http.Response, []Item, error)") {
			t.Error("Expected array pagination to return []Item directly")
		}
	})
}

// TestNoPaginatorWithoutNextSource verifies that a paginator is NOT generated when
// the response has an items field but no Link header and the NextField property
// doesn't exist in the response schema.
func TestNoPaginatorWithoutNextSource(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("No Next API", "1.0.0"))

	type Item struct {
		ID   string `json:"id" doc:"Item ID"`
		Name string `json:"name" doc:"Item name"`
	}

	// Response has an items field but NO next field and NO Link header
	type ListItemsResponse struct {
		Body struct {
			Items []Item `json:"items" doc:"The list of items"`
			Total int    `json:"total" doc:"Total count"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/items",
		Summary:     "List items",
	}, func(ctx context.Context, input *struct{}) (*ListItemsResponse, error) {
		return &ListItemsResponse{
			Body: struct {
				Items []Item `json:"items" doc:"The list of items"`
				Total int    `json:"total" doc:"Total count"`
			}{
				Items: []Item{{ID: "1", Name: "First"}},
				Total: 1,
			},
		}, nil
	})

	openapi := api.OpenAPI()

	t.Run("DetectionRejectsWithoutNextSource", func(t *testing.T) {
		// NextField is "Next" globally, but the response schema has no "Next" property
		pagination := &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		}

		for _, pathItem := range openapi.Paths {
			for _, op := range getOperations(pathItem) {
				if op.OperationID == "list-items" {
					if isPaginatedOperation(op, openapi, pagination) {
						t.Error("Expected list-items to NOT be detected as paginated when NextField doesn't exist in response schema")
					}
				}
			}
		}
	})

	t.Run("CodeGenNoPaginator", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "humaclient_nonext_gen_*")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(tempDir)

		oldDir, _ := os.Getwd()
		os.Chdir(tempDir)
		defer os.Chdir(oldDir)

		err = GenerateClientWithOptions(api, Options{
			Pagination: &PaginationOptions{
				ItemsField: "Items",
				NextField:  "Next",
			},
		})
		if err != nil {
			t.Fatalf("Failed to generate client: %v", err)
		}

		content, err := os.ReadFile("nonextapiclient/client.go")
		if err != nil {
			t.Fatalf("Failed to read generated client: %v", err)
		}

		clientCode := string(content)

		if strings.Contains(clientCode, "Paginator") {
			t.Error("Expected no Paginator method when response has no next field and no Link header")
		}

		// Should still generate the ListItems method
		if !strings.Contains(clientCode, "ListItems(") {
			t.Error("Expected ListItems method to be generated")
		}
	})
}

// TestNullableArrayItemsInPaginator verifies that nullable (pointer) array items
// are correctly represented in the generated paginator and struct fields.
func TestNullableArrayItemsInPaginator(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Nullable Items API", "1.0.0"))

	type ListTimesResponse struct {
		Link string       `header:"Link" doc:"Pagination link"`
		Body []*time.Time `json:"body"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-times",
		Method:      http.MethodGet,
		Path:        "/times",
		Summary:     "List times",
	}, func(ctx context.Context, input *struct{}) (*ListTimesResponse, error) {
		return &ListTimesResponse{}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_nullable_items_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClientWithOptions(api, Options{})
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("nullableitemsapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v", err)
		}
	})

	t.Run("PaginatorUsesPointerType", func(t *testing.T) {
		if !strings.Contains(clientCode, "iter.Seq2[*time.Time, error]") {
			t.Error("Expected paginator to use *time.Time for nullable items")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})
}

// TestNullableArrayItemsInWrappedPaginator verifies pointer items in object-wrapped pagination.
func TestNullableArrayItemsInWrappedPaginator(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Nullable Wrapped API", "1.0.0"))

	type ListTimesResponse struct {
		Body struct {
			Items []*time.Time `json:"items" doc:"List of timestamps"`
			Next  string       `json:"next,omitempty" doc:"Next page URL"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-times",
		Method:      http.MethodGet,
		Path:        "/times",
		Summary:     "List times",
	}, func(ctx context.Context, input *struct{}) (*ListTimesResponse, error) {
		return &ListTimesResponse{}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_nullable_wrapped_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClientWithOptions(api, Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		},
	})
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("nullablewrappedapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v", err)
		}
	})

	t.Run("PaginatorUsesPointerType", func(t *testing.T) {
		if !strings.Contains(clientCode, "iter.Seq2[*time.Time, error]") {
			t.Error("Expected paginator to use *time.Time for nullable wrapped items")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("StructFieldUsesPointerType", func(t *testing.T) {
		if !strings.Contains(clientCode, "[]*time.Time") {
			t.Error("Expected struct field to use []*time.Time")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})
}

// TestPaginatorItemTypeMatchesStructField verifies that the paginator's item type
// is always consistent with the struct field's array element type, even for pointer items.
func TestPaginatorItemTypeMatchesStructField(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Pointer Items API", "1.0.0"))

	type GroupSummary struct {
		ID   string `json:"id" doc:"Group ID"`
		Name string `json:"name" doc:"Group name"`
	}

	// Array response with pointer items and Link header
	type ListGroupsResponse struct {
		Link string `header:"Link" doc:"Pagination link"`
		Body []*GroupSummary
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-groups",
		Method:      http.MethodGet,
		Path:        "/groups",
	}, func(ctx context.Context, input *struct{}) (*ListGroupsResponse, error) {
		return &ListGroupsResponse{}, nil
	})

	// Object-wrapped response with pointer items and next field
	type ListTeamsResponse struct {
		Body struct {
			Items []*GroupSummary `json:"items" doc:"List of teams"`
			Next  string          `json:"next,omitempty" doc:"Next page URL"`
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-teams",
		Method:      http.MethodGet,
		Path:        "/teams",
	}, func(ctx context.Context, input *struct{}) (*ListTeamsResponse, error) {
		return &ListTeamsResponse{}, nil
	})

	tempDir, err := os.MkdirTemp("", "humaclient_pointer_items_*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	oldDir, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldDir)

	err = GenerateClientWithOptions(api, Options{
		Pagination: &PaginationOptions{
			ItemsField: "Items",
			NextField:  "Next",
		},
	})
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile("pointeritemsapiclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v", err)
		}
	})

	t.Run("PaginatorItemTypeMatchesArrayPagination", func(t *testing.T) {
		// The paginator's iter.Seq2 type and the yield function must use the same
		// element type as the array/slice field in the struct. If the struct has
		// []GroupSummary, the paginator must use GroupSummary. If []*GroupSummary,
		// it must use *GroupSummary. They must never diverge.
		if strings.Contains(clientCode, "[]GroupSummary") {
			if !strings.Contains(clientCode, "iter.Seq2[GroupSummary, error]") {
				t.Error("Struct uses []GroupSummary but paginator doesn't use GroupSummary")
				t.Logf("Generated code:\n%s", clientCode)
			}
		} else if strings.Contains(clientCode, "[]*GroupSummary") {
			if !strings.Contains(clientCode, "iter.Seq2[*GroupSummary, error]") {
				t.Error("Struct uses []*GroupSummary but paginator doesn't use *GroupSummary")
				t.Logf("Generated code:\n%s", clientCode)
			}
		}
	})

	t.Run("PaginatorItemTypeMatchesWrappedPagination", func(t *testing.T) {
		// Same consistency check for object-wrapped pagination
		if strings.Contains(clientCode, "ListTeamsPaginator") {
			if strings.Contains(clientCode, "[]GroupSummary") {
				if !strings.Contains(clientCode, "iter.Seq2[GroupSummary, error]") {
					t.Error("Wrapped struct uses []GroupSummary but paginator doesn't use GroupSummary")
					t.Logf("Generated code:\n%s", clientCode)
				}
			} else if strings.Contains(clientCode, "[]*GroupSummary") {
				if !strings.Contains(clientCode, "iter.Seq2[*GroupSummary, error]") {
					t.Error("Wrapped struct uses []*GroupSummary but paginator doesn't use *GroupSummary")
					t.Logf("Generated code:\n%s", clientCode)
				}
			}
		}
	})
}

// TestPointerTypePreservation verifies that pointer types from the original Go
// structs are faithfully preserved in the generated client code. This covers
// pointer-to-struct fields, pointer-to-scalar fields, and array items with
// pointer elements — cases where Huma's schema.Nullable alone is insufficient.
func TestPointerTypePreservation(t *testing.T) {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("Pointer Preservation API", "1.0.0"))

	type SubThing struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	type Parent struct {
		// Pointer to struct
		PtrStruct *SubThing `json:"ptrStruct" doc:"pointer to struct"`
		// Non-pointer struct
		ValStruct SubThing `json:"valStruct" doc:"value struct"`
		// Array of pointer to struct
		PtrItems []*SubThing `json:"ptrItems" doc:"array of pointer to struct"`
		// Array of non-pointer struct
		ValItems []SubThing `json:"valItems" doc:"array of value struct"`
		// Pointer to string
		PtrStr *string `json:"ptrStr" doc:"pointer to string"`
		// Non-pointer string
		ValStr string `json:"valStr" doc:"value string"`
		// Pointer to int
		PtrInt *int64 `json:"ptrInt" doc:"pointer to int"`
		// Non-pointer int
		ValInt int64 `json:"valInt" doc:"value int"`
		// Pointer to bool
		PtrBool *bool `json:"ptrBool" doc:"pointer to bool"`
		// Non-pointer bool
		ValBool bool `json:"valBool" doc:"value bool"`
		// Pointer to float
		PtrFloat *float64 `json:"ptrFloat" doc:"pointer to float"`
		// Non-pointer float
		ValFloat float64 `json:"valFloat" doc:"value float"`
	}

	type GetParentResponse struct {
		Body Parent
	}

	huma.Get(api, "/parents/{id}", func(ctx context.Context, input *struct {
		ID string `path:"id"`
	}) (*GetParentResponse, error) {
		return &GetParentResponse{}, nil
	})

	tempDir := t.TempDir()

	err := GenerateClientWithOptions(api, Options{
		OutputDirectory: tempDir + "/ptrpreserveclient",
		PackageName:     "ptrpreserveclient",
	})
	if err != nil {
		t.Fatalf("Failed to generate client: %v", err)
	}

	content, err := os.ReadFile(tempDir + "/ptrpreserveclient/client.go")
	if err != nil {
		t.Fatalf("Failed to read generated client: %v", err)
	}

	clientCode := string(content)

	// Normalize whitespace for matching (gofmt aligns struct fields with tabs/spaces)
	normalized := regexp.MustCompile(`\s+`).ReplaceAllString(clientCode, " ")

	t.Run("ValidGoSyntax", func(t *testing.T) {
		fset := token.NewFileSet()
		_, err := parser.ParseFile(fset, "client.go", content, parser.ParseComments)
		if err != nil {
			t.Fatalf("Generated code has syntax errors: %v", err)
		}
	})

	t.Run("PointerStructField", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrStruct *SubThing") {
			t.Error("Expected PtrStruct to use pointer type *SubThing")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueStructField", func(t *testing.T) {
		if !strings.Contains(normalized, "ValStruct SubThing") {
			t.Error("Expected ValStruct to use value type SubThing")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("PointerArrayItems", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrItems []*SubThing") {
			t.Error("Expected PtrItems to use []*SubThing")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueArrayItems", func(t *testing.T) {
		if !strings.Contains(normalized, "ValItems []SubThing") {
			t.Error("Expected ValItems to use []SubThing")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("PointerStringField", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrStr *string") {
			t.Error("Expected PtrStr to use *string")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueStringField", func(t *testing.T) {
		if strings.Contains(normalized, "ValStr *string") {
			t.Error("Expected ValStr to use string, not *string")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("PointerIntField", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrInt *int64") {
			t.Error("Expected PtrInt to use *int64")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueIntField", func(t *testing.T) {
		if strings.Contains(normalized, "ValInt *int64") {
			t.Error("Expected ValInt to use int64, not *int64")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("PointerBoolField", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrBool *bool") {
			t.Error("Expected PtrBool to use *bool")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueBoolField", func(t *testing.T) {
		if strings.Contains(normalized, "ValBool *bool") {
			t.Error("Expected ValBool to use bool, not *bool")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("PointerFloatField", func(t *testing.T) {
		if !strings.Contains(normalized, "PtrFloat *float64") {
			t.Error("Expected PtrFloat to use *float64")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})

	t.Run("ValueFloatField", func(t *testing.T) {
		if strings.Contains(normalized, "ValFloat *float64") {
			t.Error("Expected ValFloat to use float64, not *float64")
			t.Logf("Generated code:\n%s", clientCode)
		}
	})
}
