package humaclient

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/danielgtaylor/casing"
	"github.com/danielgtaylor/huma/v2"
)

// PaginationOptions configures how wrapped list responses are paginated.
// When set on Options, the generator will detect object responses containing
// an array field and generate paginator methods that iterate through items.
type PaginationOptions struct {
	// ItemsField is the Go struct field name in the response object that
	// contains the array of items (e.g. "Items", "Data", "Results").
	// Must be a root-level field.
	ItemsField string

	// NextField is the Go struct field path in the response object that
	// contains the URL for the next page. Supports dot-separated paths for
	// nested fields (e.g. "Next", "Meta.Next", "Pagination.NextURL").
	// Optional. When set, enables body-based pagination in addition to or
	// instead of Link header pagination.
	NextField string
}

// Options provides customization options for client generation
type Options struct {
	PackageName     string             // Custom package name (default: generated from API title)
	ClientName      string             // Custom client interface name (default: generated from API title)
	AllowedPackages []string           // List of allowed Go packages that can be referenced instead of recreated
	OutputDirectory string             // Custom output directory (default: package name in current directory)
	Pagination      *PaginationOptions // Pagination options for object-wrapped list responses
}

// ClientTemplateData holds data for client code generation
type ClientTemplateData struct {
	PackageName         string
	ClientInterfaceName string
	ClientStructName    string
	Imports             []string
	ExternalImports     []string // External packages that are allowed to be referenced
	Schemas             []SchemaData
	Operations          []OperationData
	RequestOptionFields []OptionField
	HasRequestBodies    bool
	HasMergePatch       bool // Whether any operation supports both merge-patch+json and json-patch+json (autopatch)
	HasJSONPatchOp      bool // Whether JSONPatchOp already exists as a schema struct
}

// SchemaData represents an OpenAPI schema for code generation
type SchemaData struct {
	Name         string
	StructName   string
	Fields       []FieldData
	IsExternal   bool   // Whether this schema comes from an external allowed package
	ExternalType string // Full external type name (e.g., "huma.Schema")
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
	HasResponseBody   bool
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
	IsMergePatch      bool   // Whether this operation has both merge-patch and json-patch media types (autopatch detection)
	ItemsField        string // Go struct field name for items array in wrapped responses (e.g. "Items")
	NextField         string // Go struct field path for next-page URL (e.g. "Next" or "Meta.Next")
	NextFieldNilCheck string // Nil-check expression for nullable intermediate fields in NextField path
	ResponseType      string // Wrapper struct type name for object-wrapped paginated responses
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
	outputDir := opts.OutputDirectory
	if outputDir == "" {
		outputDir = packageName
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Generate client code
	code, err := generateClientCode(openapi, packageName, outputDir, opts)
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
	if len(parts) > 0 {
		parts[0] = strings.ToLower(parts[0])
	}
	return strings.Join(parts, "")
}

// generateClientCode generates the complete client code for an OpenAPI specification
func generateClientCode(openapi *huma.OpenAPI, packageName string, outputDir string, opts Options) ([]byte, error) {
	data, err := buildTemplateData(openapi, packageName, outputDir, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build template data: %w", err)
	}

	funcMap := template.FuncMap{
		"join":       strings.Join,
		"lower":      strings.ToLower,
		"trimPrefix": strings.TrimPrefix,
		"trimSuffix": strings.TrimSuffix,
		"hasPrefix":  strings.HasPrefix,
		"eq":         func(a, b any) bool { return a == b },
	}

	tmpl, err := template.New("client").Funcs(funcMap).Parse(clientTemplate)
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
func buildTemplateData(openapi *huma.OpenAPI, packageName string, outputDir string, opts Options) (*ClientTemplateData, error) {
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
	if hasPaginatedOperations(openapi, opts.Pagination) {
		data.Imports = append(data.Imports, "\"iter\"")
	}

	sort.Strings(data.Imports)

	// Determine the current package import path from the output directory
	currentPkgPath := determineCurrentPackagePath(outputDir)

	// Generate schemas
	externalImports := make(map[string]bool)
	if err := buildSchemas(&data.Schemas, openapi, opts.AllowedPackages, externalImports, currentPkgPath); err != nil {
		return nil, fmt.Errorf("failed to build schemas: %w", err)
	}

	// Generate operations
	if err := buildOperations(&data.Operations, &data.RequestOptionFields, openapi, opts.AllowedPackages, externalImports, currentPkgPath, opts.Pagination); err != nil {
		return nil, fmt.Errorf("failed to build operations: %w", err)
	}

	// Check if any operations use merge-patch (Patchable) body types
	for _, op := range data.Operations {
		if op.IsMergePatch {
			data.HasMergePatch = true
			break
		}
	}

	// Check if JSONPatchOp already exists as a non-external schema struct (e.g. from Huma autopatch)
	for _, s := range data.Schemas {
		if s.StructName == "JSONPatchOp" && !s.IsExternal {
			data.HasJSONPatchOp = true
			break
		}
	}

	// Add standard library imports that were collected as external imports
	var keysToDelete []string
	for pkg := range externalImports {
		switch pkg {
		case "time", "net":
			// Add standard library imports to main imports
			data.Imports = append(data.Imports, "\""+pkg+"\"")
			keysToDelete = append(keysToDelete, pkg)
		}
	}
	// Remove standard library imports from external imports
	for _, pkg := range keysToDelete {
		delete(externalImports, pkg)
	}
	sort.Strings(data.Imports)

	// Add external imports to the imports list, excluding self-imports
	for pkg := range externalImports {
		// Skip imports that would create a circular reference to the current package
		if pkg != currentPkgPath {
			data.ExternalImports = append(data.ExternalImports, "\""+pkg+"\"")
		}
	}
	sort.Strings(data.ExternalImports)

	return data, nil
}

// determineCurrentPackagePath attempts to determine the import path for the current package
// based on the output directory
func determineCurrentPackagePath(outputDir string) string {
	// Convert output directory to absolute path
	absPath, err := filepath.Abs(outputDir)
	if err != nil {
		return ""
	}

	// Try to find the current working directory and determine the module root
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Look for go.mod file to determine module root and module path
	moduleRoot, modulePath := findGoModule(wd)
	if moduleRoot == "" || modulePath == "" {
		return ""
	}

	// Calculate relative path from module root to output directory
	relPath, err := filepath.Rel(moduleRoot, absPath)
	if err != nil {
		return ""
	}

	// Construct the full import path
	if relPath == "." {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(relPath)
}

// findGoModule walks up the directory tree to find go.mod and extract module path
func findGoModule(startDir string) (moduleRoot, modulePath string) {
	dir := startDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			// Found go.mod, read module path
			content, err := os.ReadFile(goModPath)
			if err != nil {
				return "", ""
			}

			// Parse module directive (first line typically: "module path/to/module")
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					modulePath := strings.TrimSpace(strings.TrimPrefix(line, "module"))
					return dir, modulePath
				}
			}
			return dir, ""
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory
			break
		}
		dir = parent
	}
	return "", ""
}

// buildSchemas generates schema data from OpenAPI components
func buildSchemas(schemas *[]SchemaData, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) error {
	if openapi.Components == nil || openapi.Components.Schemas == nil {
		return nil
	}

	schemaMap := openapi.Components.Schemas.Map()
	names := getSortedKeys(schemaMap)

	for _, name := range names {
		schema := schemaMap[name]
		if schema.Type == "object" {
			schemaData, err := createSchemaData(name, schema, openapi, allowedPackages, externalImports, currentPkgPath)
			if err != nil {
				return fmt.Errorf("failed to create schema data for %s: %w", name, err)
			}
			*schemas = append(*schemas, schemaData)
		}
	}

	return nil
}

// getSortedKeys returns sorted keys from a schema map
func getSortedKeys(schemaMap map[string]*huma.Schema) []string {
	names := make([]string, 0, len(schemaMap))
	for name := range schemaMap {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// createSchemaData creates a SchemaData from a schema name and definition
func createSchemaData(name string, schema *huma.Schema, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) (SchemaData, error) {
	schemaData := SchemaData{
		Name:       name,
		StructName: casing.Camel(name, casing.Initialism),
	}

	// Check if this schema comes from an allowed external package
	pkg, typeName := getExternalPackageAndType(openapi, name, allowedPackages)
	if pkg != "" {
		schemaData.IsExternal = true
		schemaData.ExternalType = typeName
		externalImports[pkg] = true
	} else {
		// Only build fields for internal types
		if err := buildFields(&schemaData.Fields, name, schema, openapi, allowedPackages, externalImports, currentPkgPath); err != nil {
			return schemaData, err
		}
	}

	return schemaData, nil
}

// getExternalPackageAndType checks if a schema name corresponds to an external type from an allowed package
func getExternalPackageAndType(openapi *huma.OpenAPI, schemaName string, allowedPackages []string) (packageName string, typeName string) {
	if openapi.Components == nil || openapi.Components.Schemas == nil {
		return "", ""
	}

	// Use TypeFromRef to get the Go type info
	goType := openapi.Components.Schemas.TypeFromRef("#/components/schemas/" + schemaName)
	if goType == nil {
		return "", ""
	}

	// Extract package path and type name from the reflect.Type
	pkg := goType.PkgPath()
	name := goType.Name()

	if pkg == "" || name == "" {
		return "", ""
	}

	// Check if the package is in the allowed list
	for _, allowedPkg := range allowedPackages {
		if pkg == allowedPkg {
			// Get the package name from the import path (last segment)
			parts := strings.Split(pkg, "/")
			pkgName := parts[len(parts)-1]

			// Handle versioned packages like /v2, /v3, etc.
			if strings.HasPrefix(pkgName, "v") && len(pkgName) > 1 {
				// For versioned packages, use the parent directory + version (e.g., huma instead of v2)
				if len(parts) > 1 {
					pkgName = parts[len(parts)-2]
				}
			}

			return pkg, pkgName + "." + name
		}
	}

	return "", ""
}

// buildFields generates field data from schema properties
func buildFields(fields *[]FieldData, parentSchemaName string, schema *huma.Schema, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) error {
	if schema.Properties == nil {
		return nil
	}

	propNames := getSortedPropertyNames(schema.Properties)

	for _, propName := range propNames {
		if strings.HasPrefix(propName, "$") {
			continue // Skip invalid Go field names
		}

		propSchema := schema.Properties[propName]
		field := createFieldData(propName, propSchema, parentSchemaName, schema, openapi, allowedPackages, externalImports, currentPkgPath)
		*fields = append(*fields, field)
	}

	return nil
}

// getSortedPropertyNames returns sorted property names
func getSortedPropertyNames(properties map[string]*huma.Schema) []string {
	propNames := make([]string, 0, len(properties))
	for propName := range properties {
		propNames = append(propNames, propName)
	}
	sort.Strings(propNames)
	return propNames
}

// createFieldData creates a FieldData from property information.
// It uses the Huma schema registry's reflect.Type info to faithfully
// reproduce the original Go type's pointer semantics.
func createFieldData(propName string, propSchema *huma.Schema, parentSchemaName string, parentSchema *huma.Schema, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) FieldData {
	fieldName := casing.Camel(propName, casing.Initialism)

	// Use reflect type as the authoritative source for pointer detection.
	// This handles cases where Huma doesn't set schema.Nullable (e.g. *Struct).
	fieldIsPointer, elemIsPointer := getFieldNullability(parentSchemaName, propName, openapi)

	// Determine nullable: the field was originally a pointer, or is a circular
	// reference, or is a non-required object/$ref (for proper omitempty behavior).
	nullable := fieldIsPointer || isCircularReference(propSchema, parentSchema, openapi)
	if !nullable && !isRequired(parentSchema, propName) {
		resolved := resolveSchema(propSchema, openapi)
		if propSchema.Ref != "" || resolved.Type == "object" {
			nullable = true
		}
	}

	goType := schemaToGoType(propSchema, openapi, nullable, allowedPackages, externalImports, currentPkgPath)

	// Fix array element pointers: if the original Go slice had pointer elements
	// (e.g. []*Thing) but the schema doesn't carry that info, patch the type.
	if elemIsPointer && strings.HasPrefix(goType, "[]") && !strings.HasPrefix(goType, "[]*") {
		goType = "[]*" + goType[2:]
	}

	// Use omitempty for non-required fields or fields that were originally pointers
	jsonTag := propName
	if !isRequired(parentSchema, propName) || fieldIsPointer {
		jsonTag += ",omitempty"
	}

	return FieldData{
		Name:     fieldName,
		Type:     goType,
		JSONTag:  jsonTag,
		HumaTags: buildHumaTags(propSchema),
	}
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
func buildOperations(operations *[]OperationData, globalOptions *[]OptionField, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string, pagination *PaginationOptions) error {
	allOptions := make(map[string]OptionField)

	paths := getSortedPaths(openapi.Paths)

	for _, path := range paths {
		pathItem := openapi.Paths[path]
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			operation := getOperationForMethod(pathItem, method)
			if operation == nil || operation.Responses == nil {
				continue
			}

			opData, err := createOperationData(operation, method, path, openapi, allowedPackages, externalImports, allOptions, currentPkgPath, pagination)
			if err != nil {
				return fmt.Errorf("failed to create operation data for %s %s: %w", method, path, err)
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

// getSortedPaths returns sorted paths from the paths map
func getSortedPaths(paths map[string]*huma.PathItem) []string {
	pathList := make([]string, 0, len(paths))
	for path := range paths {
		pathList = append(pathList, path)
	}
	sort.Strings(pathList)
	return pathList
}

// getOperationForMethod returns the operation for a given HTTP method
func getOperationForMethod(pathItem *huma.PathItem, method string) *huma.Operation {
	switch method {
	case "GET":
		return pathItem.Get
	case "POST":
		return pathItem.Post
	case "PUT":
		return pathItem.Put
	case "DELETE":
		return pathItem.Delete
	case "PATCH":
		return pathItem.Patch
	default:
		return nil
	}
}

// createOperationData creates an OperationData from operation details
func createOperationData(operation *huma.Operation, method, path string, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, allOptions map[string]OptionField, currentPkgPath string, pagination *PaginationOptions) (OperationData, error) {
	opData := OperationData{
		MethodName: generateMethodName(operation),
		HTTPMethod: method,
		Path:       path,
		PathParams: extractPathParams(path),
	}

	// Handle request body
	handleRequestBody(&opData, operation, openapi, allowedPackages, externalImports, currentPkgPath)

	// Handle return type
	opData.ReturnType, opData.ZeroValue, opData.HasResponseBody = generateReturnType(operation, openapi, allowedPackages, externalImports, currentPkgPath)

	// Check for pagination support
	handlePagination(&opData, operation, openapi, allowedPackages, externalImports, currentPkgPath, pagination)

	// Handle parameters
	if err := buildOperationParams(&opData, operation, allOptions, openapi, allowedPackages, externalImports, currentPkgPath); err != nil {
		return opData, err
	}

	return opData, nil
}

// handleRequestBody processes request body for operation data
func handleRequestBody(opData *OperationData, operation *huma.Operation, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) {
	if operation.RequestBody == nil || operation.RequestBody.Content == nil {
		return
	}

	// Check for standard JSON content type first
	if jsonContent := operation.RequestBody.Content["application/json"]; jsonContent != nil {
		opData.HasRequestBody = true
		opData.RequestBodyType = schemaToGoType(jsonContent.Schema, openapi, false, allowedPackages, externalImports, currentPkgPath)
		opData.HasOptionalBody = !operation.RequestBody.Required
		return
	}

	// Check for autopatch: requires both merge-patch and JSON patch content types.
	// This distinguishes Huma autopatch from manually defined PATCH operations
	// that may use a single content type with a typed schema.
	if operation.RequestBody.Content["application/merge-patch+json"] != nil &&
		operation.RequestBody.Content["application/json-patch+json"] != nil {
		opData.IsMergePatch = true
		opData.HasRequestBody = true
		opData.RequestBodyType = "Patchable"
	}
}

// handlePagination processes pagination info for operation data
func handlePagination(opData *OperationData, operation *huma.Operation, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string, pagination *PaginationOptions) {
	if !isPaginatedOperation(operation, openapi, pagination) {
		return
	}

	for statusCode, response := range operation.Responses {
		if statusCode[0] != '2' || response.Content == nil {
			continue
		}
		jsonContent := response.Content["application/json"]
		if jsonContent == nil || jsonContent.Schema == nil {
			continue
		}
		resolved := resolveSchema(jsonContent.Schema, openapi)

		// Array response pagination (existing behavior)
		if resolved.Type == "array" && resolved.Items != nil {
			opData.IsPaginated = true
			opData.ItemType = schemaToGoType(resolved.Items, openapi, false, allowedPackages, externalImports, currentPkgPath)
			// Check if the original Go type has pointer elements via reflect
			// (only if schema.Nullable didn't already produce a pointer type)
			if !strings.HasPrefix(opData.ItemType, "*") && isSliceOfPointers(jsonContent.Schema.Ref, openapi) {
				opData.ItemType = "*" + opData.ItemType
			}
			return
		}

		// Object-wrapped response pagination
		if resolved.Type == "object" && pagination != nil && pagination.ItemsField != "" {
			for propName, propSchema := range resolved.Properties {
				goName := casing.Camel(propName, casing.Initialism)
				if goName == pagination.ItemsField && propSchema.Type == "array" && propSchema.Items != nil {
					// Verify pagination source exists: Link header or NextField property
					hasLinkHeader := response.Headers != nil && response.Headers["Link"] != nil
					hasNextField := false
					var nextFieldNilChecks []string
					if pagination.NextField != "" {
						var valid bool
						valid, nextFieldNilChecks = validateNextFieldPath(resolved, pagination.NextField, openapi)
						hasNextField = valid
					}
					if !hasLinkHeader && !hasNextField {
						continue
					}

					opData.IsPaginated = true
					opData.ItemType = schemaToGoType(propSchema.Items, openapi, false, allowedPackages, externalImports, currentPkgPath)
					// Check if array items are pointers using reflect type info
					// (only if schema.Nullable didn't already produce a pointer type)
					if !strings.HasPrefix(opData.ItemType, "*") && jsonContent.Schema.Ref != "" {
						parts := strings.Split(jsonContent.Schema.Ref, "/")
						parentSchemaName := parts[len(parts)-1]
						_, elemIsPointer := getFieldNullability(parentSchemaName, propName, openapi)
						if elemIsPointer {
							opData.ItemType = "*" + opData.ItemType
						}
					}
					opData.ItemsField = pagination.ItemsField
					if hasNextField {
						opData.NextField = pagination.NextField
						if len(nextFieldNilChecks) > 0 {
							opData.NextFieldNilCheck = strings.Join(nextFieldNilChecks, " && ")
						}
					}
					opData.ResponseType = schemaToGoType(jsonContent.Schema, openapi, false, allowedPackages, externalImports, currentPkgPath)
					return
				}
			}
		}
	}
}

// buildOperationParams builds parameter data for an operation
func buildOperationParams(opData *OperationData, operation *huma.Operation, allOptions map[string]OptionField, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) error {
	if operation.Parameters == nil {
		return nil
	}

	for _, param := range operation.Parameters {
		paramData := createParamData(param, openapi, allowedPackages, externalImports, currentPkgPath)

		switch param.In {
		case "query":
			opData.HasQueryParams = true
			opData.QueryParams = append(opData.QueryParams, paramData)
			addToOptionsIfNotExists(allOptions, &opData.OptionsFields, paramData, "query")

		case "header":
			opData.HasHeaderParams = true
			opData.HeaderParams = append(opData.HeaderParams, paramData)
			addToOptionsIfNotExists(allOptions, &opData.OptionsFields, paramData, "header")
		}
	}

	// Generate operation-specific options struct name if needed
	if len(opData.OptionsFields) > 0 {
		opData.OptionsStructName = opData.MethodName + "Options"
	}

	return nil
}

// createParamData creates a ParamData from a huma parameter
func createParamData(param *huma.Param, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) ParamData {
	paramData := ParamData{
		Name:             param.Name,
		GoName:           casing.Camel(param.Name, casing.Initialism),
		GoNameLowerCamel: LowerCamel(param.Name, casing.Initialism),
		Type:             "string", // Most parameters are strings
		Required:         param.Required,
	}

	if param.Schema != nil {
		paramData.Type = schemaToGoType(param.Schema, openapi, false, allowedPackages, externalImports, currentPkgPath)
	}

	return paramData
}

// addToOptionsIfNotExists adds a parameter to options if it doesn't already exist
func addToOptionsIfNotExists(allOptions map[string]OptionField, operationOptions *[]OptionField, paramData ParamData, paramIn string) {
	optKey := fmt.Sprintf("%s_%s", paramData.GoName, paramIn)
	if _, exists := allOptions[optKey]; !exists {
		optField := OptionField{
			Name:     paramData.GoName,
			Type:     paramData.Type,
			JSONName: paramData.Name,
			Tag:      fmt.Sprintf("`json:\"%s,omitempty\"`", paramData.Name),
			In:       paramIn,
		}
		allOptions[optKey] = optField
		*operationOptions = append(*operationOptions, optField)
	}
}

// hasRequestBodies checks if any operation in the API has request bodies
func hasRequestBodies(openapi *huma.OpenAPI) bool {
	return hasAnyOperation(openapi, func(op *huma.Operation) bool {
		if op.RequestBody == nil || op.RequestBody.Content == nil {
			return false
		}
		return op.RequestBody.Content["application/json"] != nil ||
			(op.RequestBody.Content["application/merge-patch+json"] != nil &&
				op.RequestBody.Content["application/json-patch+json"] != nil)
	})
}

// hasPaginatedOperations checks if any operation in the API supports pagination
func hasPaginatedOperations(openapi *huma.OpenAPI, pagination *PaginationOptions) bool {
	return hasAnyOperation(openapi, func(op *huma.Operation) bool {
		return isPaginatedOperation(op, openapi, pagination)
	})
}

// hasAnyOperation checks if any operation in the API satisfies the given condition
func hasAnyOperation(openapi *huma.OpenAPI, condition func(*huma.Operation) bool) bool {
	for _, pathItem := range openapi.Paths {
		for _, operation := range getOperations(pathItem) {
			if condition(operation) {
				return true
			}
		}
	}
	return false
}

// resolveSchema resolves a schema reference to the actual schema using the
// registry's SchemaFromRef. If the schema is not a reference, it is returned as-is.
func resolveSchema(schema *huma.Schema, openapi *huma.OpenAPI) *huma.Schema {
	if schema.Ref == "" {
		return schema
	}
	if openapi == nil || openapi.Components == nil || openapi.Components.Schemas == nil {
		return schema
	}
	if resolved := openapi.Components.Schemas.SchemaFromRef(schema.Ref); resolved != nil {
		return resolved
	}
	return schema
}

// validateNextFieldPath validates the full dot-separated NextField path against
// a schema, verifying each intermediate segment is an object and the final
// segment is a string. Returns whether the path is valid and any nil-check
// expressions needed for nullable intermediate fields.
func validateNextFieldPath(schema *huma.Schema, path string, openapi *huma.OpenAPI) (valid bool, nilChecks []string) {
	segments := strings.Split(path, ".")
	current := schema
	prefix := "result"

	for i, segment := range segments {
		found := false
		for propName, propSchema := range current.Properties {
			goName := casing.Camel(propName, casing.Initialism)
			if goName == segment {
				resolved := resolveSchema(propSchema, openapi)
				if i < len(segments)-1 {
					// Intermediate segment: must be an object
					if resolved.Type != "object" {
						return false, nil
					}
					if propSchema.Nullable {
						nilChecks = append(nilChecks, prefix+"."+segment+" != nil")
					}
					prefix += "." + segment
					current = resolved
				} else {
					// Final segment: must be a string
					if resolved.Type != "string" {
						return false, nil
					}
				}
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nilChecks
}

// isPaginatedOperation checks if an operation supports pagination
func isPaginatedOperation(operation *huma.Operation, openapi *huma.OpenAPI, pagination *PaginationOptions) bool {
	if operation.Responses == nil {
		return false
	}

	for statusCode, response := range operation.Responses {
		if statusCode[0] != '2' || response.Content == nil {
			continue
		}
		jsonContent := response.Content["application/json"]
		if jsonContent == nil || jsonContent.Schema == nil {
			continue
		}
		schema := resolveSchema(jsonContent.Schema, openapi)

		// Array response with Link header (existing behavior)
		if schema.Type == "array" {
			if response.Headers != nil {
				if _, ok := response.Headers["Link"]; ok {
					return true
				}
			}
		}

		// Object-wrapped response with configured items field
		if schema.Type == "object" && pagination != nil && pagination.ItemsField != "" {
			hasItemsArray := false
			for propName, propSchema := range schema.Properties {
				goName := casing.Camel(propName, casing.Initialism)
				if goName == pagination.ItemsField && propSchema.Type == "array" && propSchema.Items != nil {
					hasItemsArray = true
					break
				}
			}
			if hasItemsArray {
				// Need either a Link header or a configured NextField that exists in this schema
				hasLinkHeader := response.Headers != nil && response.Headers["Link"] != nil
				if hasLinkHeader {
					return true
				}
				if pagination.NextField != "" {
					if valid, _ := validateNextFieldPath(schema, pagination.NextField, openapi); valid {
						return true
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
func generateReturnType(operation *huma.Operation, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) (string, string, bool) {
	// Find the success response (200, 201, etc.)
	var responseSchema *huma.Schema
	hasResponseBody := false
	for statusCode, response := range operation.Responses {
		if statusCode[0] == '2' && response.Content != nil {
			if jsonContent := response.Content["application/json"]; jsonContent != nil {
				responseSchema = jsonContent.Schema
				hasResponseBody = true
				break
			}
		}
	}

	if !hasResponseBody {
		// No response body, return only (*http.Response, error)
		return "(*http.Response, error)", "", false
	}

	responseType := "any"
	if responseSchema != nil {
		responseType = schemaToGoType(responseSchema, openapi, false, allowedPackages, externalImports, currentPkgPath)
	}

	zeroValue := getZeroValue(responseType)
	returnType := fmt.Sprintf("(*http.Response, %s, error)", responseType)

	return returnType, zeroValue, true
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

// getFieldNullability uses the Huma schema registry's Go reflect.Type to
// determine whether a struct field was originally a pointer (and for slice
// fields, whether the slice elements were pointers). This is the authoritative
// source because Huma does not set schema.Nullable for pointer-to-object types.
func getFieldNullability(parentSchemaName, jsonPropName string, openapi *huma.OpenAPI) (fieldIsPointer, elemIsPointer bool) {
	if openapi == nil || openapi.Components == nil || openapi.Components.Schemas == nil {
		return false, false
	}
	goType := openapi.Components.Schemas.TypeFromRef("#/components/schemas/" + parentSchemaName)
	if goType == nil {
		return false, false
	}
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	if goType.Kind() != reflect.Struct {
		return false, false
	}
	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		jsonTag := field.Tag.Get("json")
		jsonName, _, _ := strings.Cut(jsonTag, ",")
		if jsonName == jsonPropName {
			ft := field.Type
			fieldIsPointer = ft.Kind() == reflect.Pointer
			if ft.Kind() == reflect.Slice {
				elemIsPointer = ft.Elem().Kind() == reflect.Pointer
			}
			return
		}
	}
	return false, false
}

// isSliceOfPointers checks if a schema ref points to a Go slice type whose
// elements are pointers. Used by pagination to detect e.g. []*Thing.
func isSliceOfPointers(ref string, openapi *huma.OpenAPI) bool {
	if ref == "" || openapi == nil || openapi.Components == nil || openapi.Components.Schemas == nil {
		return false
	}
	goType := openapi.Components.Schemas.TypeFromRef(ref)
	if goType == nil {
		return false
	}
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	return goType.Kind() == reflect.Slice && goType.Elem().Kind() == reflect.Pointer
}

// schemaToGoType converts an OpenAPI schema to a Go type string. The nullable
// parameter is a caller-provided hint (e.g. from reflect type info or circular
// reference detection). The function also checks the schema's own Nullable flag
// and the resolved schema's Nullable flag, so callers don't need to resolve
// $ref or inspect Nullable themselves.
func schemaToGoType(schema *huma.Schema, openapi *huma.OpenAPI, nullable bool, allowedPackages []string, externalImports map[string]bool, currentPkgPath string) string {
	// Check the schema's own Nullable flag (including on $ref wrappers)
	if schema.Nullable {
		nullable = true
	}
	// Also check the resolved schema's Nullable flag
	resolved := resolveSchema(schema, openapi)
	if resolved.Nullable {
		nullable = true
	}

	// Handle references first
	if schema.Ref != "" {
		return handleReference(schema.Ref, openapi, allowedPackages, externalImports, nullable, currentPkgPath)
	}

	switch schema.Type {
	case "string":
		return handleStringType(schema.Format, externalImports, nullable)
	case "integer":
		return handleIntegerType(schema.Format, nullable)
	case "number":
		return handleNumberType(schema.Format, nullable)
	case "boolean":
		if nullable {
			return "*bool"
		}
		return "bool"
	case "array":
		if schema.Items != nil {
			return "[]" + schemaToGoType(schema.Items, openapi, false, allowedPackages, externalImports, currentPkgPath)
		}
		return "[]any"
	case "object":
		// Note: $ref objects are already handled above the switch via handleReference
		return "map[string]any"
	default:
		return "any"
	}
}

// handleStringType handles string type conversions with format consideration
func handleStringType(format string, externalImports map[string]bool, nullable bool) string {
	switch format {
	case "date-time", "datetime":
		if externalImports != nil {
			externalImports["time"] = true
		}
		if nullable {
			return "*time.Time"
		}
		return "time.Time"
	case "ipv4", "ipv6", "ip":
		if externalImports != nil {
			externalImports["net"] = true
		}
		return "net.IP" // net.IP can be nil naturally
	default:
		if nullable {
			return "*string"
		}
		return "string"
	}
}

// handleIntegerType handles integer type conversions with format consideration
func handleIntegerType(format string, nullable bool) string {
	formatMap := map[string]string{
		"int64":  "int64",
		"int32":  "int32",
		"int16":  "int16",
		"int8":   "int8",
		"uint64": "uint64",
		"uint32": "uint32",
		"uint16": "uint16",
		"uint8":  "uint8",
	}
	base := "int"
	if goType, exists := formatMap[format]; exists {
		base = goType
	}
	if nullable {
		return "*" + base
	}
	return base
}

// handleNumberType handles number type conversions
func handleNumberType(format string, nullable bool) string {
	base := "float64"
	if format == "float" {
		base = "float32"
	}
	if nullable {
		return "*" + base
	}
	return base
}

// handleReference handles schema references
func handleReference(ref string, openapi *huma.OpenAPI, allowedPackages []string, externalImports map[string]bool, isNullable bool, currentPkgPath string) string {
	parts := strings.Split(ref, "/")
	if len(parts) == 0 {
		return "any"
	}
	refName := parts[len(parts)-1]

	// Check for formatted string references
	if goType := checkFormattedStringReference(refName, openapi, externalImports, isNullable); goType != "" {
		return goType
	}

	// Check if this is an external type
	pkg, extTypeName := getExternalPackageAndType(openapi, refName, allowedPackages)
	if pkg != "" {
		// Check if this is a self-import (the external package is the current package)
		if pkg == currentPkgPath {
			// Use local type name without qualification since it's in the same package
			typeName := casing.Camel(refName, casing.Initialism)
			if isNullable {
				return "*" + typeName
			}
			return typeName
		}

		// External type from a different package
		externalImports[pkg] = true
		if isNullable {
			return "*" + extTypeName
		}
		return extTypeName
	}

	// Use local type name
	typeName := casing.Camel(refName, casing.Initialism)
	if isNullable {
		return "*" + typeName
	}
	return typeName
}

// checkFormattedStringReference checks if a reference is a formatted string
func checkFormattedStringReference(refName string, openapi *huma.OpenAPI, externalImports map[string]bool, isNullable bool) string {
	if openapi == nil || openapi.Components == nil || openapi.Components.Schemas == nil {
		return ""
	}

	schemaMap := openapi.Components.Schemas.Map()
	referencedSchema, exists := schemaMap[refName]
	if !exists || referencedSchema.Type != "string" || referencedSchema.Format == "" {
		return ""
	}

	return handleStringType(referencedSchema.Format, externalImports, isNullable)
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
{{- range .ExternalImports}}
	{{.}}
{{- end}}
)

{{/* Generate structs from schemas */}}
{{- range .Schemas}}
{{- if not .IsExternal}}
// {{.StructName}} represents the {{.Name}} schema
type {{.StructName}} struct {
{{- range .Fields}}
	{{.Name}} {{.Type}} ` + "`json:\"{{.JSONTag}}\"{{.HumaTags}}`" + `
{{- end}}
}
{{- end}}
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
{{if .HasMergePatch}}
// Patchable is an interface for types that can be used as PATCH request bodies.
// It is implemented by MergePatch and JSONPatch.
type Patchable interface {
	PatchContentType() string
}

// MergePatch represents a JSON Merge Patch (RFC 7396) document.
// Fields set to non-nil values will be updated; fields set to nil will be removed.
type MergePatch map[string]any

func (m MergePatch) PatchContentType() string { return "application/merge-patch+json" }

{{if not .HasJSONPatchOp}}
// JSONPatchOp represents a single JSON Patch (RFC 6902) operation.
type JSONPatchOp struct {
	Op    string ` + "`json:\"op\" doc:\"Operation name\" enum:\"add,remove,replace,move,copy,test\"`" + `
	Path  string ` + "`json:\"path\" doc:\"JSON Pointer to the field being operated on\"`" + `
	From  string ` + "`json:\"from,omitempty\" doc:\"JSON Pointer for the source of a move or copy\"`" + `
	Value any    ` + "`json:\"value,omitempty\" doc:\"The value to set\"`" + `
}
{{end}}
// JSONPatch represents a JSON Patch (RFC 6902) document.
type JSONPatch []JSONPatchOp

func (j JSONPatch) PatchContentType() string { return "application/json-patch+json" }

// WithIfMatch sets the If-Match header for conditional requests.
// Use this with an ETag value from a previous GET to enable optimistic locking.
func WithIfMatch(etag string) Option {
	return WithHeader("If-Match", etag)
}

// WithIfNoneMatch sets the If-None-Match header for conditional requests.
func WithIfNoneMatch(etag string) Option {
	return WithHeader("If-None-Match", etag)
}
{{end}}
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
{{- else if eq .Type "bool"}}
	if o.{{.Name}} {
{{- else if eq .Type "time.Time"}}
	if !o.{{.Name}}.IsZero() {
{{- else if hasPrefix .Type "*"}}
	if o.{{.Name}} != nil {
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
{{- if .HasResponseBody}}
		return nil, {{.ZeroValue}}, fmt.Errorf("invalid URL: %w", err)
{{- else}}
		return nil, fmt.Errorf("invalid URL: %w", err)
{{- end}}
	}

	// Apply query parameters
	reqOpts.applyQueryParams(u)

	// Prepare request body
	var reqBody io.Reader
{{- if .HasRequestBody}}
	jsonData, err := json.Marshal(body)
	if err != nil {
{{- if .HasResponseBody}}
		return nil, {{.ZeroValue}}, fmt.Errorf("failed to marshal request body: %w", err)
{{- else}}
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
{{- end}}
	}
	reqBody = bytes.NewReader(jsonData)
{{- else if .HasOptionalBody}}
	if reqOpts.Body != nil {
		jsonData, err := json.Marshal(reqOpts.Body)
		if err != nil {
{{- if .HasResponseBody}}
			return nil, {{.ZeroValue}}, fmt.Errorf("failed to marshal request body: %w", err)
{{- else}}
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
{{- end}}
		}
		reqBody = bytes.NewReader(jsonData)
	}
{{- end}}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "{{.HTTPMethod}}", u.String(), reqBody)
	if err != nil {
{{- if .HasResponseBody}}
		return nil, {{.ZeroValue}}, fmt.Errorf("failed to create request: %w", err)
{{- else}}
		return nil, fmt.Errorf("failed to create request: %w", err)
{{- end}}
	}

	// Set content type and apply custom headers
{{- if or .HasRequestBody .HasOptionalBody}}
	if reqBody != nil {
{{- if .IsMergePatch}}
		if body != nil {
			req.Header.Set("Content-Type", body.PatchContentType())
		}
{{- else}}
		req.Header.Set("Content-Type", "application/json")
{{- end}}
	}
{{- end}}
	reqOpts.applyHeaders(req)

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
{{- if .HasResponseBody}}
		return nil, {{.ZeroValue}}, fmt.Errorf("request failed: %w", err)
{{- else}}
		return nil, fmt.Errorf("request failed: %w", err)
{{- end}}
	}
	defer resp.Body.Close()

	// Handle error responses
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
{{- if .HasResponseBody}}
		return resp, {{.ZeroValue}}, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
{{- else}}
		return resp, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
{{- end}}
	}

{{- if .HasResponseBody}}
	// Parse response body
	var result {{trimPrefix (trimSuffix .ReturnType ", error)") "(*http.Response, "}}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return resp, {{.ZeroValue}}, fmt.Errorf("failed to decode response: %w", err)
	}

	return resp, result, nil
{{- else}}
	return resp, nil
{{- end}}
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
{{- if .ResponseType}}
		resp, result, err := c.{{.MethodName}}(ctx{{range .PathParams}}, {{.GoNameLowerCamel}}{{end}}, opts...)
{{- else}}
		resp, items, err := c.{{.MethodName}}(ctx{{range .PathParams}}, {{.GoNameLowerCamel}}{{end}}, opts...)
{{- end}}
		if err != nil {
			var zero {{.ItemType}}
			if !yield(zero, err) {
				return
			}
			return
		}

		// Yield all items from the first page
{{- if .ResponseType}}
		for _, item := range result.{{.ItemsField}} {
{{- else}}
		for _, item := range items {
{{- end}}
			if !yield(item, nil) {
				return
			}
		}

		// Follow pagination links
		for {
			nextURL := ""
			// Check for Link header with rel=next
			linkHeader := resp.Header.Get("Link")
			if linkHeader != "" {
				nextURL = parseLinkHeader(linkHeader, "next")
			}
{{- if .NextField}}

			// Fall back to body field for next page URL
			if nextURL == ""{{if .NextFieldNilCheck}} && {{.NextFieldNilCheck}}{{end}} && result.{{.NextField}} != "" {
				nextURL = result.{{.NextField}}
			}
{{- end}}

			if nextURL == "" {
				break
			}

			// Fetch next page using Follow method
{{- if .ResponseType}}
			var nextResult {{.ResponseType}}
			resp, err = c.Follow(ctx, nextURL, &nextResult, opts...)
{{- else}}
			var nextItems []{{.ItemType}}
			resp, err = c.Follow(ctx, nextURL, &nextItems, opts...)
{{- end}}
			if err != nil {
				var zero {{.ItemType}}
				if !yield(zero, err) {
					return
				}
				return
			}

			// Yield all items from this page
{{- if .ResponseType}}
			for _, item := range nextResult.{{.ItemsField}} {
{{- else}}
			for _, item := range nextItems {
{{- end}}
				if !yield(item, nil) {
					return
				}
			}
{{- if .ResponseType}}

			result = nextResult
{{- end}}
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
