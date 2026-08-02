// Command normalizespec preprocesses the vendored Convex OpenAPI document so
// that oapi-codegen can consume it.
//
// The upstream document at https://api.convex.dev/v1/openapi.json declares a
// number of path parameters with no "schema" and no "content" key, which is
// invalid per the OpenAPI spec and makes code generation fail outright. Every
// such parameter, however, appears elsewhere in the same document in a
// well-formed operation, so we backfill the missing schema from those.
//
// The vendored input document is left untouched; the normalized copy is
// written alongside it and is the file codegen actually reads.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
)

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintf(os.Stderr, "usage: %s <input.json> <output.json>\n", os.Args[0])
		os.Exit(2)
	}

	if err := run(os.Args[1], os.Args[2]); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "normalizespec: %v\n", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	raw, err := os.ReadFile(in)

	if err != nil {
		return err
	}

	// Decode into a generic map so we round-trip every key we do not care
	// about, rather than modelling the whole OpenAPI document.
	var doc map[string]any

	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", in, err)
	}

	params := collectParams(doc)
	known, err := knownSchemas(params)

	if err != nil {
		return err
	}

	var patched []string

	for _, p := range params {
		if _, ok := p["schema"]; ok {
			continue
		}

		if _, ok := p["content"]; ok {
			continue
		}

		name, _ := p["name"].(string)
		schema, ok := known[name]

		if !ok {
			return fmt.Errorf("parameter %q has no schema and no well-formed occurrence to copy one from", name)
		}

		p["schema"] = schema
		patched = append(patched, name)
	}

	body, err := json.MarshalIndent(doc, "", "  ")

	if err != nil {
		return err
	}

	if err := os.WriteFile(out, append(body, '\n'), 0o644); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "normalizespec: backfilled %d parameter schema(s) -> %s\n", len(patched), out)
	return nil
}

// collectParams returns every parameter object in the document, from both
// path-item level and operation level.
func collectParams(doc map[string]any) []map[string]any {
	var found []map[string]any

	appendFrom := func(holder map[string]any) {
		list, ok := holder["parameters"].([]any)

		if !ok {
			return
		}

		for _, item := range list {
			if p, ok := item.(map[string]any); ok {
				found = append(found, p)
			}
		}
	}

	paths, ok := doc["paths"].(map[string]any)

	if !ok {
		return nil
	}

	// Iterate in sorted order so any error we report is deterministic.
	for _, key := range sortedKeys(paths) {
		pathItem, ok := paths[key].(map[string]any)

		if !ok {
			continue
		}

		appendFrom(pathItem)

		for _, opKey := range sortedKeys(pathItem) {
			if op, ok := pathItem[opKey].(map[string]any); ok {
				appendFrom(op)
			}
		}
	}

	return found
}

// knownSchemas maps a parameter name to the schema used by well-formed
// occurrences of that parameter. A name whose occurrences disagree is omitted,
// so that we never silently pick one of several conflicting schemas.
func knownSchemas(params []map[string]any) (map[string]any, error) {
	type entry struct {
		schema   any
		conflict bool
	}

	seen := map[string]*entry{}

	for _, p := range params {
		schema, ok := p["schema"]

		if !ok {
			continue
		}

		name, _ := p["name"].(string)

		if e, ok := seen[name]; ok {
			if !reflect.DeepEqual(e.schema, schema) {
				e.conflict = true
			}

			continue
		}

		seen[name] = &entry{schema: schema}
	}

	result := map[string]any{}

	for name, e := range seen {
		if !e.conflict {
			result[name] = e.schema
		}
	}

	return result, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))

	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	return keys
}
