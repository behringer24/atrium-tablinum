package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
)

// portableSchema derives the schema from the Go type and replaces `"type": ["null", X]`
// with `anyOf: [{type: null}, {type: X}]`. This is equivalent but is also understood by
// clients that only know a single `type` per schema (e.g. Gemini).
// If the derivation fails, the SDK's default behavior stays (nil).
func portableSchema[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		return nil
	}
	splitNullable(s)
	return s
}

func splitNullable(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	for _, d := range s.Defs {
		splitNullable(d)
	}
	for _, p := range s.Properties {
		splitNullable(p)
	}
	splitNullable(s.Items)
	splitNullable(s.AdditionalProperties)
	for _, a := range s.AnyOf {
		splitNullable(a)
	}

	if len(s.Types) != 2 {
		return
	}
	other := ""
	for _, t := range s.Types {
		if t != "null" {
			other = t
		}
	}
	if other == "" || s.Types[0] != "null" && s.Types[1] != "null" {
		return
	}
	branch := *s
	branch.Types = nil
	branch.Type = other
	branch.Description = ""
	*s = jsonschema.Schema{
		Description: s.Description,
		AnyOf:       []*jsonschema.Schema{{Type: "null"}, &branch},
	}
}
