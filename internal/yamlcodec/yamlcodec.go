package yamlcodec

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/yaml"
)

func Marshal(message proto.Message) ([]byte, error) {
	jsonData, err := protojson.MarshalOptions{UseProtoNames: true, Indent: "  "}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf as JSON: %w", err)
	}
	data, err := yaml.JSONToYAML(jsonData)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON as YAML: %w", err)
	}
	return data, nil
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
