package api

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"creatorinsight/backend-go/internal/config"

	"github.com/goccy/go-yaml"
)

var openAPIMethods = map[string]struct{}{
	"get": {}, "post": {}, "put": {}, "patch": {}, "delete": {}, "head": {}, "options": {}, "trace": {},
}

func TestOpenAPIContractMatchesGinRoutes(t *testing.T) {
	contractPath := filepath.Join("..", "..", "..", "docs", "openapi.yaml")
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	validateOpenAPIStructure(t, document)
	validateLocalReferences(t, document)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load router config: %v", err)
	}
	router := NewRouter(RouterDeps{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	want := documentedOperations(t, document)
	got := make(map[string]struct{})
	for _, route := range router.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/") {
			continue
		}
		got[strings.ToUpper(route.Method)+" "+openAPIPath(route.Path)] = struct{}{}
	}

	missingFromContract := setDifference(got, want)
	missingFromRouter := setDifference(want, got)
	if len(missingFromContract) > 0 || len(missingFromRouter) > 0 {
		t.Fatalf("OpenAPI/Gin route drift\nmissing from OpenAPI: %v\nmissing from Gin: %v", missingFromContract, missingFromRouter)
	}
}

func validateOpenAPIStructure(t *testing.T, document map[string]any) {
	t.Helper()
	version, ok := document["openapi"].(string)
	if !ok || !strings.HasPrefix(version, "3.") {
		t.Fatalf("unsupported or missing OpenAPI version: %v", document["openapi"])
	}
	if _, ok := document["info"].(map[string]any); !ok {
		t.Fatal("OpenAPI info object is required")
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("OpenAPI paths object is required")
	}
	for path, rawItem := range paths {
		if !strings.HasPrefix(path, "/") {
			t.Errorf("OpenAPI path must begin with '/': %s", path)
		}
		item, ok := rawItem.(map[string]any)
		if !ok {
			t.Errorf("path item %s must be an object", path)
			continue
		}
		for method, rawOperation := range item {
			if _, isMethod := openAPIMethods[strings.ToLower(method)]; !isMethod {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				t.Errorf("operation %s %s must be an object", strings.ToUpper(method), path)
				continue
			}
			responses, ok := operation["responses"].(map[string]any)
			if !ok || len(responses) == 0 {
				t.Errorf("operation %s %s must define responses", strings.ToUpper(method), path)
			}
		}
	}
}

func validateLocalReferences(t *testing.T, document map[string]any) {
	t.Helper()
	var walk func(any, string)
	walk = func(value any, location string) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				childLocation := location + "/" + key
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/") {
						t.Errorf("%s contains unsupported reference %v", childLocation, child)
						continue
					}
					if _, ok := resolveJSONPointer(document, strings.TrimPrefix(ref, "#/")); !ok {
						t.Errorf("%s contains unresolved reference %s", childLocation, ref)
					}
				}
				walk(child, childLocation)
			}
		case []any:
			for index, child := range typed {
				walk(child, fmt.Sprintf("%s/%d", location, index))
			}
		}
	}
	walk(document, "#")
}

func resolveJSONPointer(document map[string]any, pointer string) (any, bool) {
	var current any = document
	for _, part := range strings.Split(pointer, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func documentedOperations(t *testing.T, document map[string]any) map[string]struct{} {
	t.Helper()
	paths := document["paths"].(map[string]any)
	operations := make(map[string]struct{})
	for path, rawItem := range paths {
		item := rawItem.(map[string]any)
		for method := range item {
			if _, ok := openAPIMethods[strings.ToLower(method)]; ok {
				operations[strings.ToUpper(method)+" "+path] = struct{}{}
			}
		}
	}
	return operations
}

func openAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[index] = "{" + strings.TrimPrefix(part, ":") + "}"
		}
	}
	return strings.Join(parts, "/")
}

func setDifference(left, right map[string]struct{}) []string {
	difference := make([]string, 0)
	for value := range left {
		if _, ok := right[value]; !ok {
			difference = append(difference, value)
		}
	}
	sort.Strings(difference)
	return difference
}
