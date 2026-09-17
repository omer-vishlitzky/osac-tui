package yamlcodec

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"sigs.k8s.io/yaml"
)

func Marshal(message proto.Message) ([]byte, error) {
	jsonData, err := protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf as JSON: %w", err)
	}
	return jsonToYAML(jsonData)
}

func MarshalTemplate(message proto.Message) ([]byte, error) {
	var data strings.Builder
	descriptor := message.ProtoReflect().Descriptor()
	for _, name := range []protoreflect.Name{"metadata", "spec"} {
		field := descriptor.Fields().ByName(name)
		if field == nil || field.Kind() != protoreflect.MessageKind {
			continue
		}
		writeTemplateField(&data, field, 0)
	}
	return []byte(data.String()), nil
}

func jsonToYAML(jsonData []byte) ([]byte, error) {
	data, err := yaml.JSONToYAML(jsonData)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON as YAML: %w", err)
	}
	return data, nil
}

func writeTemplateField(data *strings.Builder, field protoreflect.FieldDescriptor, indent int) {
	prefix := strings.Repeat(" ", indent)
	if field.ContainingOneof() != nil {
		data.WriteString(prefix + "# " + string(field.Name()) + ": " + templateValue(field) + "\n")
		return
	}
	data.WriteString(prefix + string(field.Name()) + ":")
	if field.IsMap() {
		data.WriteString(" {}\n")
		return
	}
	if field.Cardinality() == protoreflect.Repeated {
		data.WriteString(" []\n")
		return
	}
	if field.Kind() != protoreflect.MessageKind {
		data.WriteString(" " + templateValue(field) + "\n")
		return
	}
	if strings.HasPrefix(string(field.Message().FullName()), "google.protobuf.") {
		data.WriteString(" null\n")
		return
	}
	if field.Message().Fields().Len() == 0 {
		data.WriteString(" {}\n")
		return
	}
	data.WriteByte('\n')
	fields := field.Message().Fields()
	for index := 0; index < fields.Len(); index++ {
		child := fields.Get(index)
		if isMetadataServerField(field.Message(), child) {
			continue
		}
		writeTemplateField(data, child, indent+2)
	}
}

func isMetadataServerField(message protoreflect.MessageDescriptor, field protoreflect.FieldDescriptor) bool {
	if string(message.FullName()) != "osac.public.v1.Metadata" {
		return false
	}
	for _, name := range []protoreflect.Name{"creation_timestamp", "deletion_timestamp", "creator", "tenant", "version"} {
		if field.Name() == name {
			return true
		}
	}
	return false
}

func templateValue(field protoreflect.FieldDescriptor) string {
	switch field.Kind() {
	case protoreflect.MessageKind:
		return "{}"
	case protoreflect.BoolKind:
		return "false"
	case protoreflect.EnumKind:
		return string(field.Enum().Values().Get(0).Name())
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		return "0"
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return "0"
	case protoreflect.StringKind, protoreflect.BytesKind:
		return `""`
	default:
		panic("unsupported protobuf template field kind: " + field.Kind().String())
	}
}

func Unmarshal(data []byte, message proto.Message) error {
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(jsonData, message); err != nil {
		return fmt.Errorf("decode YAML as protobuf: %w", err)
	}
	return nil
}

// RedactSensitive masks scalar values in fields that commonly contain credentials.
// The original message remains available for editing; this is only for display.
func RedactSensitive(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	redactMapIndent := -1
	for index, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		if redactMapIndent >= 0 {
			if indent <= redactMapIndent {
				redactMapIndent = -1
			} else {
				lines[index] = line[:colon+1] + " \"REDACTED\""
				continue
			}
		}
		if key == "data" && strings.TrimSpace(line[colon+1:]) == "" {
			redactMapIndent = indent
			continue
		}
		if !sensitiveField(key) {
			continue
		}
		value := strings.TrimSpace(line[colon+1:])
		if value != "" && value != "{}" && value != "[]" {
			lines[index] = line[:colon+1] + " \"REDACTED\""
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func sensitiveField(key string) bool {
	key = strings.Trim(key, "\"'")
	if key == "data" || key == "string_data" {
		return true
	}
	for _, part := range []string{"password", "token", "secret", "private_key", "credential"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
