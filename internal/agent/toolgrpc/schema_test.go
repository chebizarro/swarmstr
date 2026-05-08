package toolgrpc

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestProtoMessageToJSONSchemaHandlesProtoTypes(t *testing.T) {
	files, err := protodesc.NewFiles(complexDescriptorSet())
	if err != nil {
		t.Fatalf("NewFiles: %v", err)
	}
	desc, err := files.FindDescriptorByName("schema.v1.Complex")
	if err != nil {
		t.Fatalf("FindDescriptorByName: %v", err)
	}
	msg := desc.(protoreflect.MessageDescriptor)
	schema := ProtoMessageToJSONSchema(msg)

	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("unexpected message schema: %#v", schema)
	}
	props := schema["properties"].(map[string]any)
	assertType(t, props["name"], "string")
	assertType(t, props["active"], "boolean")
	assertType(t, props["score"], "number")
	assertType(t, props["payload"], "string")
	if props["payload"].(map[string]any)["contentEncoding"] != "base64" {
		t.Fatalf("bytes should use base64 schema: %#v", props["payload"])
	}
	if got := props["bigCount"].(map[string]any); got["type"] != "string" || got["pattern"] != "^-?[0-9]+$" {
		t.Fatalf("int64 should be decimal string schema: %#v", got)
	}
	if got := props["unsignedCount"].(map[string]any); got["type"] != "string" || got["pattern"] != "^[0-9]+$" {
		t.Fatalf("uint64 should be decimal string schema: %#v", got)
	}
	if got := props["status"].(map[string]any); got["type"] != "string" || len(got["enum"].([]string)) != 2 {
		t.Fatalf("enum should be string enum schema: %#v", got)
	}
	if got := props["nullValue"].(map[string]any); got["type"] != "null" {
		t.Fatalf("google.protobuf.NullValue should be literal null schema: %#v", got)
	}
	if got := props["tags"].(map[string]any); got["type"] != "array" || got["items"].(map[string]any)["type"] != "string" {
		t.Fatalf("repeated string should be array schema: %#v", got)
	}
	if got := props["counts"].(map[string]any); got["type"] != "object" || got["additionalProperties"].(map[string]any)["type"] != "string" {
		t.Fatalf("map<string,int64> should be object/additionalProperties schema: %#v", got)
	}
	if got := props["createdAt"].(map[string]any); got["type"] != "string" || got["format"] != "date-time" {
		t.Fatalf("timestamp should be date-time string schema: %#v", got)
	}
	if got := props["attributes"].(map[string]any); got["type"] != "object" || got["additionalProperties"] != true {
		t.Fatalf("Struct should be free-form object schema: %#v", got)
	}
	if got := props["wrappedInt64"].(map[string]any); got["oneOf"] == nil {
		t.Fatalf("wrapper should be nullable: %#v", got)
	}
	if _, ok := schema["allOf"]; !ok {
		t.Fatalf("real oneof should add allOf constraints: %#v", schema)
	}
}

func TestWellKnownSchemaDirect(t *testing.T) {
	if got := ProtoMessageToJSONSchema(timestamppb.File_google_protobuf_timestamp_proto.Messages().ByName("Timestamp")); got["format"] != "date-time" {
		t.Fatalf("timestamp schema: %#v", got)
	}
	if got := ProtoMessageToJSONSchema(wrapperspb.File_google_protobuf_wrappers_proto.Messages().ByName("StringValue")); got["oneOf"] == nil {
		t.Fatalf("wrapper schema: %#v", got)
	}
}

func assertType(t *testing.T, raw any, typ string) {
	t.Helper()
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected schema map, got %#v", raw)
	}
	if m["type"] != typ {
		t.Fatalf("expected type %q, got %#v", typ, m)
	}
}

func complexDescriptorSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(timestamppb.File_google_protobuf_timestamp_proto),
		protodesc.ToFileDescriptorProto(structpb.File_google_protobuf_struct_proto),
		protodesc.ToFileDescriptorProto(wrapperspb.File_google_protobuf_wrappers_proto),
		{
			Name:       proto.String("schema/v1/complex.proto"),
			Package:    proto.String("schema.v1"),
			Syntax:     proto.String("proto3"),
			Dependency: []string{"google/protobuf/timestamp.proto", "google/protobuf/struct.proto", "google/protobuf/wrappers.proto"},
			EnumType: []*descriptorpb.EnumDescriptorProto{{
				Name: proto.String("Status"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("STATUS_UNKNOWN"), Number: proto.Int32(0)},
					{Name: proto.String("STATUS_ACTIVE"), Number: proto.Int32(1)},
				},
			}},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("Complex"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("active", 2, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					field("score", 3, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE),
					field("payload", 4, descriptorpb.FieldDescriptorProto_TYPE_BYTES),
					field("big_count", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("unsigned_count", 6, descriptorpb.FieldDescriptorProto_TYPE_UINT64),
					{
						Name:     proto.String("status"),
						JsonName: proto.String("status"),
						Number:   proto.Int32(7),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						TypeName: proto.String(".schema.v1.Status"),
					},
					{
						Name:     proto.String("null_value"),
						JsonName: proto.String("nullValue"),
						Number:   proto.Int32(8),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						TypeName: proto.String(".google.protobuf.NullValue"),
					},
					{
						Name:     proto.String("tags"),
						JsonName: proto.String("tags"),
						Number:   proto.Int32(9),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
					{
						Name:     proto.String("counts"),
						JsonName: proto.String("counts"),
						Number:   proto.Int32(10),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".schema.v1.Complex.CountsEntry"),
					},
					{
						Name:       proto.String("choice_name"),
						JsonName:   proto.String("choiceName"),
						Number:     proto.Int32(11),
						Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:       descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						OneofIndex: proto.Int32(0),
					},
					{
						Name:       proto.String("choice_id"),
						JsonName:   proto.String("choiceId"),
						Number:     proto.Int32(12),
						Label:      descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:       descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						OneofIndex: proto.Int32(0),
					},
					messageField("created_at", 13, ".google.protobuf.Timestamp"),
					messageField("attributes", 14, ".google.protobuf.Struct"),
					messageField("wrapped_int64", 15, ".google.protobuf.Int64Value"),
				},
				NestedType: []*descriptorpb.DescriptorProto{{
					Name: proto.String("CountsEntry"),
					Field: []*descriptorpb.FieldDescriptorProto{
						field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
						field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					},
					Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
				}},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}},
			}},
		},
	}}
}

func field(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		JsonName: proto.String(camelJSONName(name)),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     typ.Enum(),
	}
}

func messageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	f := field(name, number, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE)
	f.TypeName = proto.String(typeName)
	return f
}

func camelJSONName(name string) string {
	out := ""
	upperNext := false
	for _, r := range name {
		if r == '_' {
			upperNext = true
			continue
		}
		if upperNext && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		out += string(r)
		upperNext = false
	}
	return out
}
