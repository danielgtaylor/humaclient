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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
