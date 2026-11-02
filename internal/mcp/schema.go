package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
)

// portableSchema leitet das Schema aus dem Go-Typ ab und ersetzt `"type": ["null", X]`
// durch `anyOf: [{type: null}, {type: X}]`. Das ist gleichwertig, wird aber auch von
// Clients verstanden, die nur einen einzelnen `type` je Schema kennen (z. B. Gemini).
// Schlägt die Ableitung fehl, bleibt es beim Standardverhalten des SDK (nil).
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
