package toolgrpc

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// SchemaConverter converts protobuf descriptors into provider-facing JSON Schema
// compatible with protobuf JSON mapping.
type SchemaConverter struct {
	seen map[protoreflect.FullName]int
}

func NewSchemaConverter() *SchemaConverter {
	return &SchemaConverter{seen: map[protoreflect.FullName]int{}}
}

func ProtoMessageToJSONSchema(md protoreflect.MessageDescriptor) map[string]any {
	return NewSchemaConverter().MessageToJSONSchema(md)
}

func (c *SchemaConverter) MessageToJSONSchema(md protoreflect.MessageDescriptor) map[string]any {
	if md == nil {
		return map[string]any{"type": "object", "additionalProperties": true}
	}
	if schema, ok := c.wellKnownMessageSchema(md); ok {
		return schema
	}
	name := md.FullName()
	if c.seen[name] > 0 {
		return map[string]any{
			"type":        "object",
			"description": fmt.Sprintf("Recursive protobuf message %s", name),
		}
	}
	c.seen[name]++
	defer func() { c.seen[name]-- }()

	properties := map[string]any{}
	var required []string
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		jsonName := field.JSONName()
		properties[jsonName] = c.FieldToJSONSchema(field)
		if field.Cardinality() == protoreflect.Required {
			required = append(required, jsonName)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		sort.Strings(required)
		schema["required"] = required
	}
	if oneOfGroups := oneofConstraints(md); len(oneOfGroups) > 0 {
		schema["allOf"] = oneOfGroups
	}
	return schema
}

func (c *SchemaConverter) FieldToJSONSchema(fd protoreflect.FieldDescriptor) map[string]any {
	if fd == nil {
		return map[string]any{}
	}
	if fd.IsMap() {
		return map[string]any{
			"type":                 "object",
			"additionalProperties": c.FieldToJSONSchema(fd.MapValue()),
		}
	}
	if fd.IsList() {
		return map[string]any{
			"type":  "array",
			"items": c.singleFieldSchema(fd),
		}
	}
	return c.singleFieldSchema(fd)
}

func (c *SchemaConverter) singleFieldSchema(fd protoreflect.FieldDescriptor) map[string]any {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}
	case protoreflect.EnumKind:
		if fd.Enum().FullName() == "google.protobuf.NullValue" {
			return map[string]any{"type": "null"}
		}
		return enumSchema(fd.Enum())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return map[string]any{"type": "integer", "format": "int32"}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]any{"type": "integer", "format": "uint32", "minimum": 0}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return map[string]any{
			"type":        "string",
			"pattern":     "^-?[0-9]+$",
			"description": "Protobuf int64 values use decimal strings in JSON.",
		}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return map[string]any{
			"type":        "string",
			"pattern":     "^[0-9]+$",
			"description": "Protobuf uint64 values use decimal strings in JSON.",
		}
	case protoreflect.FloatKind:
		return map[string]any{"type": "number", "format": "float"}
	case protoreflect.DoubleKind:
		return map[string]any{"type": "number", "format": "double"}
	case protoreflect.StringKind:
		return map[string]any{"type": "string"}
	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "contentEncoding": "base64"}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return c.MessageToJSONSchema(fd.Message())
	default:
		return map[string]any{}
	}
}

func enumSchema(ed protoreflect.EnumDescriptor) map[string]any {
	values := ed.Values()
	enumValues := make([]string, 0, values.Len())
	seen := map[string]struct{}{}
	for i := 0; i < values.Len(); i++ {
		name := string(values.Get(i).Name())
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		enumValues = append(enumValues, name)
	}
	sort.Strings(enumValues)
	return map[string]any{"type": "string", "enum": enumValues}
}

func oneofConstraints(md protoreflect.MessageDescriptor) []any {
	oneofs := md.Oneofs()
	var groups []any
	for i := 0; i < oneofs.Len(); i++ {
		od := oneofs.Get(i)
		if od.IsSynthetic() {
			continue
		}
		fields := od.Fields()
		if fields.Len() == 0 {
			continue
		}
		anyRequired := make([]any, 0, fields.Len())
		choices := make([]any, 0, fields.Len()+1)
		for j := 0; j < fields.Len(); j++ {
			jsonName := fields.Get(j).JSONName()
			anyRequired = append(anyRequired, map[string]any{"required": []string{jsonName}})
			choices = append(choices, map[string]any{"required": []string{jsonName}})
		}
		choices = append([]any{map[string]any{"not": map[string]any{"anyOf": anyRequired}}}, choices...)
		groups = append(groups, map[string]any{"oneOf": choices})
	}
	return groups
}

func (c *SchemaConverter) wellKnownMessageSchema(md protoreflect.MessageDescriptor) (map[string]any, bool) {
	switch string(md.FullName()) {
	case "google.protobuf.Timestamp":
		return map[string]any{"type": "string", "format": "date-time"}, true
	case "google.protobuf.Duration":
		return map[string]any{"type": "string", "pattern": "^-?[0-9]+(\\.[0-9]{1,9})?s$"}, true
	case "google.protobuf.FieldMask":
		return map[string]any{"type": "string", "description": "Comma-separated protobuf field mask paths."}, true
	case "google.protobuf.Empty":
		return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}, true
	case "google.protobuf.Any":
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"properties": map[string]any{
				"@type": map[string]any{"type": "string"},
			},
			"required": []string{"@type"},
		}, true
	case "google.protobuf.Struct":
		return map[string]any{"type": "object", "additionalProperties": true}, true
	case "google.protobuf.Value":
		return map[string]any{}, true
	case "google.protobuf.ListValue":
		return map[string]any{"type": "array", "items": map[string]any{}}, true
	case "google.protobuf.DoubleValue":
		return nullable(map[string]any{"type": "number", "format": "double"}), true
	case "google.protobuf.FloatValue":
		return nullable(map[string]any{"type": "number", "format": "float"}), true
	case "google.protobuf.Int64Value":
		return nullable(map[string]any{"type": "string", "pattern": "^-?[0-9]+$", "description": "Protobuf int64 values use decimal strings in JSON."}), true
	case "google.protobuf.UInt64Value":
		return nullable(map[string]any{"type": "string", "pattern": "^[0-9]+$", "description": "Protobuf uint64 values use decimal strings in JSON."}), true
	case "google.protobuf.Int32Value":
		return nullable(map[string]any{"type": "integer", "format": "int32"}), true
	case "google.protobuf.UInt32Value":
		return nullable(map[string]any{"type": "integer", "format": "uint32", "minimum": 0}), true
	case "google.protobuf.BoolValue":
		return nullable(map[string]any{"type": "boolean"}), true
	case "google.protobuf.StringValue":
		return nullable(map[string]any{"type": "string"}), true
	case "google.protobuf.BytesValue":
		return nullable(map[string]any{"type": "string", "contentEncoding": "base64"}), true
	default:
		return nil, false
	}
}

func nullable(schema map[string]any) map[string]any {
	return map[string]any{"oneOf": []any{schema, map[string]any{"type": "null"}}}
}
