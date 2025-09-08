package humaclient

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/danielgtaylor/casing"
	"github.com/danielgtaylor/huma/v2"
)

// Options provides customization options for client generation
type Options struct {
	PackageName string // Custom package name (default: generated from API title)
	ClientName  string // Custom client interface name (default: generated from API title)
}

// ClientTemplateData holds data for client code generation
type ClientTemplateData struct {
	PackageName         string
	ClientInterfaceName string
	ClientStructName    string
	Imports             []string
	Schemas             []SchemaData
	Operations          []OperationData
	RequestOptionFields []OptionField
	HasRequestBodies    bool
}

// SchemaData represents an OpenAPI schema for code generation
type SchemaData struct {
	Name       string
	StructName string
	Fields     []FieldData
}

// FieldData represents a field in a struct
type FieldData struct {
	Name     string
	Type     string
	JSONTag  string
	HumaTags string
}

// OperationData represents an API operation for code generation
type OperationData struct {
	MethodName        string
	HTTPMethod        string
	Path              string
	PathParams        []ParamData
	HasRequestBody    bool
	RequestBodyType   string
	HasOptionalBody   bool
	ReturnType        string
	ZeroValue         string
	HasQueryParams    bool
	HasHeaderParams   bool
	QueryParams       []ParamData
	HeaderParams      []ParamData
	OptionsStructName string
	OptionsFields     []OptionField
	IsPaginated       bool
	ItemType          string
}

// ParamData represents a parameter
type ParamData struct {
	Name             string
	GoName           string
	GoNameLowerCamel string
	Type             string
	Required         bool
}

// OptionField represents an optional parameter field
type OptionField struct {
	Name     string
	Type     string
	JSONName string
	Tag      string
	In       string // "query" or "header"
}

// Register adds a Huma API to be processed for client generation.
// This should be called after setting up your API but before starting the server.
func Register(api huma.API) {
	RegisterWithOptions(api, Options{})
}

// RegisterWithOptions adds a Huma API to be processed for client generation with custom options.
// This should be called after setting up your API but before starting the server.
func RegisterWithOptions(api huma.API, opts Options) {
	// Check if client generation is requested via environment variable
	if os.Getenv("GENERATE_CLIENT") != "" {
		if err := GenerateClientWithOptions(api, opts); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating client: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// GenerateClient generates a client SDK for the given Huma API
func GenerateClient(api huma.API) error {
	return GenerateClientWithOptions(api, Options{})
}

// GenerateClientWithOptions generates a client SDK for the given Huma API with custom options
func GenerateClientWithOptions(api huma.API, opts Options) error {
	openapi := api.OpenAPI()

	// Generate package name from API title or use custom name
	packageName := opts.PackageName
	if packageName == "" {
		packageName = strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(openapi.Info.Title, "")) + "client"
	}

	// Create output directory
	outputDir := packageName
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Generate client code
	code, err := generateClientCode(openapi, packageName, opts)
	if err != nil {
		return fmt.Errorf("failed to generate client code: %w", err)
	}

	// Write client code to file
	clientFile := filepath.Join(outputDir, "client.go")
	if err := os.WriteFile(clientFile, code, 0644); err != nil {
		return fmt.Errorf("failed to write client file: %w", err)
	}

	fmt.Printf("Generated client SDK in %s/\n", outputDir)
	return nil
}

// LowerCamel returns a lowerCamelCase version of the input.
func LowerCamel(value string, transform ...casing.TransformFunc) string {
	parts := casing.Split(casing.Camel(value, transform...))
	parts[0] = strings.ToLower(parts[0])
	return strings.Join(parts, "")
}

// generateClientCode generates the complete client code for an OpenAPI specification
func generateClientCode(openapi *huma.OpenAPI, packageName string, opts Options) ([]byte, error) {
	data, err := buildTemplateData(openapi, packageName, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build template data: %w", err)
	}

	tmpl, err := template.New("client").Funcs(template.FuncMap{
		"join":       strings.Join,
		"lower":      strings.ToLower,
		"trimPrefix": strings.TrimPrefix,
		"trimSuffix": strings.TrimSuffix,
		"eq": func(a, b any) bool {
			return a == b
		},
	}).Parse(clientTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	// Format the generated code
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to format generated code: %w", err)
	}

	return formatted, nil
}

// buildTemplateData builds the data structure for template execution
func buildTemplateData(openapi *huma.OpenAPI, packageName string, opts Options) (*ClientTemplateData, error) {
	// Generate client interface name from options or API title
	clientInterfaceName := opts.ClientName
	if clientInterfaceName == "" {
		clientInterfaceName = strings.TrimSuffix(casing.Camel(openapi.Info.Title, casing.Initialism), "Client") + "Client"
	}

	data := &ClientTemplateData{
		PackageName:         packageName,
		ClientInterfaceName: clientInterfaceName,
		HasRequestBodies:    hasRequestBodies(openapi),
	}
	data.ClientStructName = strings.TrimSuffix(data.ClientInterfaceName, "Client") + "ClientImpl"

	// Build imports list
	data.Imports = []string{
		"\"bytes\"", // Always needed for Follow method
		"\"context\"",
		"\"encoding/json\"",
		"\"fmt\"",
		"\"io\"",
		"\"net/http\"",
		"\"net/url\"",
		"\"strings\"",
	}

	// Add iter import if we have paginated operations
	if hasPaginatedOperations(openapi) {
		data.Imports = append(data.Imports, "\"iter\"")
	}

	sort.Strings(data.Imports)

	// Generate schemas
	if err := buildSchemas(&data.Schemas, openapi); err != nil {
		return nil, fmt.Errorf("failed to build schemas: %w", err)
	}

	// Generate operations
	if err := buildOperations(&data.Operations, &data.RequestOptionFields, openapi); err != nil {
		return nil, fmt.Errorf("failed to build operations: %w", err)
	}

	return data, nil
}

// buildSchemas generates schema data from OpenAPI components
func buildSchemas(schemas *[]SchemaData, openapi *huma.OpenAPI) error {
	if openapi.Components == nil || openapi.Components.Schemas == nil {
		return nil
	}

	// Sort schema names for consistent output
	schemaMap := openapi.Components.Schemas.Map()
	names := make([]string, 0, len(schemaMap))
	for name := range schemaMap {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		schema := schemaMap[name]
		if schema.Type == "object" {
			schemaData := SchemaData{
				Name:       name,
				StructName: casing.Camel(name, casing.Initialism),
			}

			if err := buildFields(&schemaData.Fields, schema, openapi); err != nil {
				return fmt.Errorf("failed to build fields for %s: %w", name, err)
			}

			*schemas = append(*schemas, schemaData)
		}
	}

	return nil
}

// buildFields generates field data from schema properties
func buildFields(fields *[]FieldData, schema *huma.Schema, openapi *huma.OpenAPI) error {
	if schema.Properties == nil {
		return nil
	}

	// Sort properties for consistent output
	propNames := make([]string, 0, len(schema.Properties))
	for propName := range schema.Properties {
		propNames = append(propNames, propName)
	}
	sort.Strings(propNames)

	for _, propName := range propNames {
		propSchema := schema.Properties[propName]
		// Skip invalid Go field names like those starting with $
		if strings.HasPrefix(propName, "$") {
			continue
		}
		fieldName := casing.Camel(propName, casing.Initialism)

		// Check if this field is nullable (schema is nullable OR field is not required)
		isFieldNullable := !isRequired(schema, propName)

		// Use pointer for circular references: if field references same type as parent OR field is nullable
		usePointer := isFieldNullable || isCircularReference(propSchema, schema, openapi)
		goType := schemaToGoTypeWithNullability(propSchema, openapi, usePointer)

		jsonTag := propName
		if isFieldNullable {
			jsonTag += ",omitempty"
		}

		// Build Huma tags from schema
		humaTags := buildHumaTags(propSchema)

		field := FieldData{
			Name:     fieldName,
			Type:     goType,
			JSONTag:  jsonTag,
			HumaTags: humaTags,
		}

		*fields = append(*fields, field)
	}

	return nil
}

// buildHumaTags builds Huma validation tags from schema
func buildHumaTags(schema *huma.Schema) string {
	var tags []string

	// Documentation
	if schema.Description != "" {
		tags = append(tags, fmt.Sprintf("doc:\"%s\"", schema.Description))
	}

	// String validation
	if schema.MinLength != nil {
		tags = append(tags, fmt.Sprintf("minLength:\"%d\"", *schema.MinLength))
	}
	if schema.MaxLength != nil {
		tags = append(tags, fmt.Sprintf("maxLength:\"%d\"", *schema.MaxLength))
	}
	if schema.Pattern != "" {
		tags = append(tags, fmt.Sprintf("pattern:\"%s\"", schema.Pattern))
	}

	// Numeric validation
	if schema.Minimum != nil {
		tags = append(tags, fmt.Sprintf("minimum:\"%g\"", *schema.Minimum))
	}
	if schema.Maximum != nil {
		tags = append(tags, fmt.Sprintf("maximum:\"%g\"", *schema.Maximum))
	}
	if schema.ExclusiveMinimum != nil {
		tags = append(tags, fmt.Sprintf("exclusiveMinimum:\"%g\"", *schema.ExclusiveMinimum))
	}
	if schema.ExclusiveMaximum != nil {
		tags = append(tags, fmt.Sprintf("exclusiveMaximum:\"%g\"", *schema.ExclusiveMaximum))
	}
	if schema.MultipleOf != nil {
		tags = append(tags, fmt.Sprintf("multipleOf:\"%g\"", *schema.MultipleOf))
	}

	// Array validation
	if schema.MinItems != nil {
		tags = append(tags, fmt.Sprintf("minItems:\"%d\"", *schema.MinItems))
	}
	if schema.MaxItems != nil {
		tags = append(tags, fmt.Sprintf("maxItems:\"%d\"", *schema.MaxItems))
	}

	// Object validation
	if schema.MinProperties != nil {
		tags = append(tags, fmt.Sprintf("minProperties:\"%d\"", *schema.MinProperties))
	}
	if schema.MaxProperties != nil {
		tags = append(tags, fmt.Sprintf("maxProperties:\"%d\"", *schema.MaxProperties))
	}

	// Enumeration
	if len(schema.Enum) > 0 {
		enumValues := make([]string, len(schema.Enum))
		for i, v := range schema.Enum {
			enumValues[i] = fmt.Sprintf("%v", v)
		}
		tags = append(tags, fmt.Sprintf("enum:\"%s\"", strings.Join(enumValues, ",")))
	}

	// Default value
	if schema.Default != nil {
		tags = append(tags, fmt.Sprintf("default:\"%v\"", schema.Default))
	}

	// Format
	if schema.Format != "" {
		tags = append(tags, fmt.Sprintf("format:\"%s\"", schema.Format))
	}

	// Example/Examples
	if len(schema.Examples) == 1 {
		tags = append(tags, fmt.Sprintf("example:\"%v\"", schema.Examples[0]))
	} else if len(schema.Examples) > 1 {
		exampleValues := make([]string, len(schema.Examples))
		for i, v := range schema.Examples {
			exampleValues[i] = fmt.Sprintf("%v", v)
		}
		tags = append(tags, fmt.Sprintf("examples:\"%s\"", strings.Join(exampleValues, ",")))
	}

	// Read/Write access control
	if schema.ReadOnly {
		tags = append(tags, "readOnly:\"true\"")
	}
	if schema.WriteOnly {
		tags = append(tags, "writeOnly:\"true\"")
	}

	// Deprecation status
	if schema.Deprecated {
		tags = append(tags, "deprecated:\"true\"")
	}

	if len(tags) > 0 {
		return " " + strings.Join(tags, " ")
	}
	return ""
}

// buildOperations generates operation data from OpenAPI paths
func buildOperations(operations *[]OperationData, globalOptions *[]OptionField, openapi *huma.OpenAPI) error {
	allOptions := make(map[string]OptionField)

	// Sort paths for consistent output
	paths := make([]string, 0, len(openapi.Paths))
	for path := range openapi.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		pathItem := openapi.Paths[path]

		// Sort methods for consistent output
		methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
		for _, method := range methods {
			var operation *huma.Operation
			switch method {
			case "GET":
				operation = pathItem.Get
			case "POST":
				operation = pathItem.Post
			case "PUT":
				operation = pathItem.Put
			case "DELETE":
				operation = pathItem.Delete
			case "PATCH":
				operation = pathItem.Patch
			}

			if operation == nil || operation.Responses == nil {
				continue
			}

			// Check if operation returns JSON
			hasJSONResponse := false
			for _, response := range operation.Responses {
				if response.Content != nil && response.Content["application/json"] != nil {
					hasJSONResponse = true
					break
				}
			}

			if !hasJSONResponse {
				continue
			}

			opData := OperationData{
				MethodName: generateMethodName(operation),
				HTTPMethod: method,
				Path:       path,
				PathParams: extractPathParams(path),
			}

			// Handle request body
			if operation.RequestBody != nil && operation.RequestBody.Content != nil {
				if jsonContent := operation.RequestBody.Content["application/json"]; jsonContent != nil {
					opData.HasRequestBody = true
					opData.RequestBodyType = schemaToGoType(jsonContent.Schema, openapi)
					opData.HasOptionalBody = !operation.RequestBody.Required
				}
			}

			// Handle return type
			opData.ReturnType, opData.ZeroValue = generateReturnType(operation, openapi)

			// Check for pagination support
			if isPaginatedOperation(operation, openapi) {
				opData.IsPaginated = true
				// Extract item type from array return type
				for statusCode, response := range operation.Responses {
					if statusCode[0] == '2' && response.Content != nil {
						if jsonContent := response.Content["application/json"]; jsonContent != nil {
							if jsonContent.Schema != nil && jsonContent.Schema.Type == "array" && jsonContent.Schema.Items != nil {
								opData.ItemType = schemaToGoType(jsonContent.Schema.Items, openapi)
								break
							}
						}
					}
				}
			}

			// Handle parameters
			if err := buildOperationParams(&opData, operation, allOptions, openapi); err != nil {
				return fmt.Errorf("failed to build params for %s %s: %w", method, path, err)
			}

			*operations = append(*operations, opData)
		}
	}

	// Convert global options map to slice and sort
	for _, opt := range allOptions {
		*globalOptions = append(*globalOptions, opt)
	}
	sort.Slice(*globalOptions, func(i, j int) bool {
		return (*globalOptions)[i].Name < (*globalOptions)[j].Name
	})

	return nil
}

// buildOperationParams builds parameter data for an operation
func buildOperationParams(opData *OperationData, operation *huma.Operation, allOptions map[string]OptionField, openapi *huma.OpenAPI) error {
	if operation.Parameters == nil {
		return nil
	}

	for _, param := range operation.Parameters {
		paramData := ParamData{
			Name:             param.Name,
			GoName:           casing.Camel(param.Name, casing.Initialism),
			GoNameLowerCamel: LowerCamel(param.Name, casing.Initialism),
			Type:             "string", // Most parameters are strings
			Required:         param.Required,
		}

		if param.Schema != nil {
			paramData.Type = schemaToGoType(param.Schema, openapi)
		}

		switch param.In {
		case "query":
			opData.HasQueryParams = true
			opData.QueryParams = append(opData.QueryParams, paramData)

			// Add to global options (avoid duplicates)
			optKey := fmt.Sprintf("%s_%s", paramData.GoName, param.In)
			if _, exists := allOptions[optKey]; !exists {
				optField := OptionField{
					Name:     paramData.GoName,
					Type:     paramData.Type,
					JSONName: param.Name,
					Tag:      fmt.Sprintf("`json:\"%s,omitempty\"`", param.Name),
					In:       "query",
				}
				allOptions[optKey] = optField
				opData.OptionsFields = append(opData.OptionsFields, optField)
			}

		case "header":
			opData.HasHeaderParams = true
			opData.HeaderParams = append(opData.HeaderParams, paramData)

			// Add to global options (avoid duplicates)
			optKey := fmt.Sprintf("%s_%s", paramData.GoName, param.In)
			if _, exists := allOptions[optKey]; !exists {
				optField := OptionField{
					Name:     paramData.GoName,
					Type:     paramData.Type,
					JSONName: param.Name,
					Tag:      fmt.Sprintf("`json:\"%s,omitempty\"`", param.Name),
					In:       "header",
				}
				allOptions[optKey] = optField
				opData.OptionsFields = append(opData.OptionsFields, optField)
			}
		}
	}

	// Generate operation-specific options struct name if needed
	if len(opData.OptionsFields) > 0 {
		opData.OptionsStructName = opData.MethodName + "Options"
	}

	return nil
}

// hasRequestBodies checks if any operation in the API has request bodies
func hasRequestBodies(openapi *huma.OpenAPI) bool {
	for _, pathItem := range openapi.Paths {
		for _, operation := range getOperations(pathItem) {
			if operation.RequestBody != nil && operation.RequestBody.Content != nil {
				if operation.RequestBody.Content["application/json"] != nil {
					return true
				}
			}
		}
	}
	return false
}

// hasPaginatedOperations checks if any operation in the API supports pagination
func hasPaginatedOperations(openapi *huma.OpenAPI) bool {
	for _, pathItem := range openapi.Paths {
		for _, operation := range getOperations(pathItem) {
			if isPaginatedOperation(operation, openapi) {
				return true
			}
		}
	}
	return false
}

// isPaginatedOperation checks if an operation supports pagination
func isPaginatedOperation(operation *huma.Operation, openapi *huma.OpenAPI) bool {
	if operation.Responses == nil {
		return false
	}

	// Check if operation returns an array and has Link header
	for statusCode, response := range operation.Responses {
		if statusCode[0] == '2' && response.Content != nil {
			if jsonContent := response.Content["application/json"]; jsonContent != nil {
				if jsonContent.Schema != nil && jsonContent.Schema.Type == "array" {
					// Check if response defines Link header
					if response.Headers != nil {
						if linkHeader := response.Headers["Link"]; linkHeader != nil {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// getOperations extracts HTTP operations from a path item
func getOperations(pathItem *huma.PathItem) map[string]*huma.Operation {
	operations := make(map[string]*huma.Operation)

	if pathItem.Get != nil {
		operations["GET"] = pathItem.Get
	}
	if pathItem.Post != nil {
		operations["POST"] = pathItem.Post
	}
	if pathItem.Put != nil {
		operations["PUT"] = pathItem.Put
	}
	if pathItem.Delete != nil {
		operations["DELETE"] = pathItem.Delete
	}
	if pathItem.Patch != nil {
		operations["PATCH"] = pathItem.Patch
	}

	return operations
}

// generateMethodName generates a Go method name from an operation
func generateMethodName(operation *huma.Operation) string {
	if operation.OperationID != "" {
		return casing.Camel(operation.OperationID, casing.Initialism)
	}

	// Fallback to generating a name from tags and summary
	if len(operation.Tags) > 0 {
		return casing.Camel(operation.Tags[0], casing.Initialism)
	}

	return "Operation"
}

// generateReturnType generates the return type for an operation method
func generateReturnType(operation *huma.Operation, openapi *huma.OpenAPI) (string, string) {
	// Find the success response (200, 201, etc.)
	var responseSchema *huma.Schema
	for statusCode, response := range operation.Responses {
		if statusCode[0] == '2' && response.Content != nil {
			if jsonContent := response.Content["application/json"]; jsonContent != nil {
				responseSchema = jsonContent.Schema
				break
			}
		}
	}

	responseType := "any"
	if responseSchema != nil {
		responseType = schemaToGoType(responseSchema, openapi)
	}

	zeroValue := getZeroValue(responseType)
	returnType := fmt.Sprintf("(*http.Response, %s, error)", responseType)

	return returnType, zeroValue
}

// extractPathParams extracts parameter names from a path template
func extractPathParams(path string) []ParamData {
	var params []ParamData
	re := regexp.MustCompile(`\{([^}]+)\}`)
	matches := re.FindAllStringSubmatch(path, -1)
	for _, match := range matches {
		if len(match) > 1 {
			paramName := match[1]
			params = append(params, ParamData{
				Name:             paramName,
				GoName:           casing.Camel(paramName, casing.Initialism),
				GoNameLowerCamel: LowerCamel(paramName, casing.Initialism),
				Type:             "string",
				Required:         true,
			})
		}
	}
	return params
}

// isCircularReference checks if a property schema references the same type as its parent schema
func isCircularReference(propSchema *huma.Schema, parentSchema *huma.Schema, openapi *huma.OpenAPI) bool {
	if propSchema.Ref == "" {
		return false
	}

	// Extract the referenced type name
	parts := strings.Split(propSchema.Ref, "/")
	if len(parts) == 0 {
		return false
	}
	refTypeName := parts[len(parts)-1]

	// Find the parent schema name in the registry
	if openapi != nil && openapi.Components != nil && openapi.Components.Schemas != nil {
		schemaMap := openapi.Components.Schemas.Map()
		for name, s := range schemaMap {
			if s == parentSchema {
				return refTypeName == name
			}
		}
	}

	return false
}

// isRequired checks if a property is required in the schema
func isRequired(schema *huma.Schema, propName string) bool {
	if schema.Required == nil {
		return false
	}
	for _, req := range schema.Required {
		if req == propName {
			return true
		}
	}
	return false
}

// schemaToGoType converts an OpenAPI schema type to a Go type
func schemaToGoType(schema *huma.Schema, openapi *huma.OpenAPI) string {
	return schemaToGoTypeWithNullability(schema, openapi, false)
}

// schemaToGoTypeWithNullability converts an OpenAPI schema type to a Go type with optional nullability
func schemaToGoTypeWithNullability(schema *huma.Schema, openapi *huma.OpenAPI, isNullable bool) string {
	switch schema.Type {
	case "string":
		return "string"
	case "integer":
		if schema.Format == "int64" {
			return "int64"
		}
		return "int"
	case "number":
		if schema.Format == "float" {
			return "float32"
		}
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		if schema.Items != nil {
			itemType := schemaToGoType(schema.Items, openapi)
			return "[]" + itemType
		}
		return "[]any"
	case "object":
		if schema.Ref != "" {
			// Extract type name from reference
			parts := strings.Split(schema.Ref, "/")
			if len(parts) > 0 {
				refName := parts[len(parts)-1]
				typeName := casing.Camel(refName, casing.Initialism)

				// If this field is nullable, use pointer to break circular references
				if isNullable {
					return "*" + typeName
				}

				return typeName
			}
		}
		return "map[string]any"
	}

	// Handle references
	if schema.Ref != "" {
		parts := strings.Split(schema.Ref, "/")
		if len(parts) > 0 {
			refName := parts[len(parts)-1]
			typeName := casing.Camel(refName, casing.Initialism)

			// If this field is nullable, use pointer to break circular references
			if isNullable {
				return "*" + typeName
			}

			return typeName
		}
	}

	return "any"
}

// getZeroValue returns the zero value for a given Go type
func getZeroValue(goType string) string {
	switch goType {
	case "string":
		return `""`
	case "int", "int8", "int16", "int32", "int64":
		return "0"
	case "uint", "uint8", "uint16", "uint32", "uint64":
		return "0"
	case "float32", "float64":
		return "0"
	case "bool":
		return "false"
	case "any":
		return "nil"
	default:
		// For pointer types, return nil
		if strings.HasPrefix(goType, "*") {
			return "nil"
		}
		// For slices, maps, return nil
		if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") {
			return "nil"
		}
		// For struct types, return zero value
		return goType + "{}"
	}
}

// clientTemplate is the text template for generating client code
const clientTemplate = `// Code generated by humaclient. DO NOT EDIT.

package {{.PackageName}}

import (
{{- range .Imports}}
	{{.}}
{{- end}}
)

{{/* Generate structs from schemas */}}
{{- range .Schemas}}
// {{.StructName}} represents the {{.Name}} schema
type {{.StructName}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} ` + "`json:\"{{.JSONTag}}\"{{.HumaTags}}`" + `
{{- end}}
}
{{end}}

{{/* Generate option function types */}}
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

// applyQueryParams applies custom query parameters to a URL
func (r *RequestOptions) applyQueryParams(u *url.URL) {
	if len(r.CustomQuery) > 0 {
		q := u.Query()
		for key, value := range r.CustomQuery {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}
}

// applyHeaders applies custom headers to an HTTP request
func (r *RequestOptions) applyHeaders(req *http.Request) {
	for key, value := range r.CustomHeaders {
		req.Header.Set(key, value)
	}
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

{{/* Generate operation-specific option structs and apply methods */}}
{{- range .Operations}}
{{- if .OptionsStructName}}
// {{.OptionsStructName}} contains optional parameters for {{.MethodName}}
type {{.OptionsStructName}} struct {
{{- range .OptionsFields}}
	{{.Name}} {{.Type}} {{.Tag}}
{{- end}}
}

// Apply implements OptionsApplier for {{.OptionsStructName}}
func (o {{.OptionsStructName}}) Apply(opts *RequestOptions) {
{{- range .OptionsFields}}
{{- if eq .Type "string"}}
	if o.{{.Name}} != "" {
{{- else}}
	if o.{{.Name}} != 0 {
{{- end}}
{{- if eq .In "query"}}
		if opts.CustomQuery == nil {
			opts.CustomQuery = make(map[string]string)
		}
{{- if eq .Type "string"}}
		opts.CustomQuery["{{.JSONName}}"] = o.{{.Name}}
{{- else}}
		opts.CustomQuery["{{.JSONName}}"] = fmt.Sprintf("%v", o.{{.Name}})
{{- end}}
{{- else}}
		if opts.CustomHeaders == nil {
			opts.CustomHeaders = make(map[string]string)
		}
{{- if eq .Type "string"}}
		opts.CustomHeaders["{{.JSONName}}"] = o.{{.Name}}
{{- else}}
		opts.CustomHeaders["{{.JSONName}}"] = fmt.Sprintf("%v", o.{{.Name}})
{{- end}}
{{- end}}
	}
{{- end}}
}
{{end}}
{{- end}}

// {{.ClientInterfaceName}} defines the interface for the API client
type {{.ClientInterfaceName}} interface {
{{- range .Operations}}
	{{.MethodName}}(ctx context.Context{{range .PathParams}}, {{.GoNameLowerCamel}} {{.Type}}{{end}}{{if .HasRequestBody}}, body {{.RequestBodyType}}{{end}}, opts ...Option) {{.ReturnType}}
{{- if .IsPaginated}}
	{{.MethodName}}Paginator(ctx context.Context{{range .PathParams}}, {{.GoNameLowerCamel}} {{.Type}}{{end}}, opts ...Option) iter.Seq2[{{.ItemType}}, error]
{{- end}}
{{- end}}
	Follow(ctx context.Context, link string, result any, opts ...Option) (*http.Response, error)
}

// {{.ClientStructName}} implements the {{.ClientInterfaceName}} interface
type {{.ClientStructName}} struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a new {{.ClientInterfaceName}} with default HTTP client
func New(baseURL string) {{.ClientInterfaceName}} {
	return NewWithClient(baseURL, nil)
}

// NewWithClient creates a new {{.ClientInterfaceName}} with custom base URL and HTTP client
func NewWithClient(baseURL string, client *http.Client) {{.ClientInterfaceName}} {
	if client == nil {
		client = &http.Client{}
	}
	return &{{.ClientStructName}}{
		baseURL:    baseURL,
		httpClient: client,
	}
}

{{/* Generate method implementations */}}
{{- range .Operations}}
// {{.MethodName}} calls the {{.HTTPMethod}} {{.Path}} endpoint
func (c *{{$.ClientStructName}}) {{.MethodName}}(ctx context.Context{{range .PathParams}}, {{.GoNameLowerCamel}} {{.Type}}{{end}}{{if .HasRequestBody}}, body {{.RequestBodyType}}{{end}}, opts ...Option) {{.ReturnType}} {
	// Apply options
	reqOpts := &RequestOptions{}
	for _, opt := range opts {
		opt(reqOpts)
	}

	// Build URL with path parameters
	pathTemplate := "{{.Path}}"
{{- range .PathParams}}
	pathTemplate = strings.ReplaceAll(pathTemplate, "{{print "{" .Name "}"}}", url.PathEscape({{.GoNameLowerCamel}}))
{{- end}}

	u, err := url.Parse(c.baseURL + pathTemplate)
	if err != nil {
		return nil, {{.ZeroValue}}, fmt.Errorf("invalid URL: %w", err)
	}

	// Apply query parameters
	reqOpts.applyQueryParams(u)

	// Prepare request body
	var reqBody io.Reader
{{- if .HasRequestBody}}
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, {{.ZeroValue}}, fmt.Errorf("failed to marshal request body: %w", err)
	}
	reqBody = bytes.NewReader(jsonData)
{{- else if .HasOptionalBody}}
	if reqOpts.Body != nil {
		jsonData, err := json.Marshal(reqOpts.Body)
		if err != nil {
			return nil, {{.ZeroValue}}, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonData)
	}
{{- end}}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "{{.HTTPMethod}}", u.String(), reqBody)
	if err != nil {
		return nil, {{.ZeroValue}}, fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type and apply custom headers
{{- if or .HasRequestBody .HasOptionalBody}}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
{{- end}}
	reqOpts.applyHeaders(req)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, {{.ZeroValue}}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle error responses
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return resp, {{.ZeroValue}}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response body
	var result {{trimPrefix (trimSuffix .ReturnType ", error)") "(*http.Response, "}}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return resp, {{.ZeroValue}}, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, result, nil
}
{{end}}

// Follow follows a link to retrieve a related resource
func (c *{{.ClientStructName}}) Follow(ctx context.Context, link string, result any, opts ...Option) (*http.Response, error) {
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

	// Apply query parameters
	reqOpts.applyQueryParams(u)

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

	// Set content type and apply custom headers
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	reqOpts.applyHeaders(req)

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

{{/* Generate paginator method implementations */}}
{{- range .Operations}}
{{- if .IsPaginated}}
// {{.MethodName}}Paginator returns an iterator that fetches all pages of {{.MethodName}} results
func (c *{{$.ClientStructName}}) {{.MethodName}}Paginator(ctx context.Context{{range .PathParams}}, {{.GoNameLowerCamel}} {{.Type}}{{end}}, opts ...Option) iter.Seq2[{{.ItemType}}, error] {
	return func(yield func({{.ItemType}}, error) bool) {
		// Start with the first page
		resp, items, err := c.{{.MethodName}}(ctx{{range .PathParams}}, {{.GoNameLowerCamel}}{{end}}{{if .HasRequestBody}}, body{{end}}, opts...)
		if err != nil {
			var zero {{.ItemType}}
			if !yield(zero, err) {
				return
			}
			return
		}

		// Yield all items from the first page
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}

		// Follow pagination links
		for {
			// Check for Link header with rel=next
			linkHeader := resp.Header.Get("Link")
			if linkHeader == "" {
				break
			}

			// Parse Link header to find next URL
			nextURL := parseLinkHeader(linkHeader, "next")
			if nextURL == "" {
				break
			}

			// Fetch next page using Follow method
			var nextItems []{{.ItemType}}
			resp, err = c.Follow(ctx, nextURL, &nextItems, opts...)
			if err != nil {
				var zero {{.ItemType}}
				if !yield(zero, err) {
					return
				}
				return
			}

			// Yield all items from this page
			for _, item := range nextItems {
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}
{{- end}}
{{- end}}

// parseLinkHeader parses a Link header and returns the URL for the specified relation
func parseLinkHeader(linkHeader, rel string) string {
	// Simple parser for Link header format: <url>; rel="next", <url2>; rel="prev"
	links := strings.Split(linkHeader, ",")
	for _, link := range links {
		link = strings.TrimSpace(link)
		parts := strings.Split(link, ";")
		if len(parts) < 2 {
			continue
		}

		url := strings.Trim(strings.TrimSpace(parts[0]), "<>")

		for _, param := range parts[1:] {
			param = strings.TrimSpace(param)
			if strings.Contains(param, "rel=") {
				// Extract rel value (handle both quoted and unquoted)
				relValue := strings.TrimPrefix(param, "rel=")
				relValue = strings.Trim(relValue, "\"'")
				if relValue == rel {
					return url
				}
			}
		}
	}
	return ""
}
`
