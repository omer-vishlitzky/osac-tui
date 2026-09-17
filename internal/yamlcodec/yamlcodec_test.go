package yamlcodec

import (
	"strings"
	"testing"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestRoundTripUsesProtoFieldNames(t *testing.T) {
	object := &publicv1.VirtualNetwork{
		Id:       "network-1",
		Metadata: &publicv1.Metadata{Name: "network-1", Version: 4},
		Spec:     &publicv1.VirtualNetworkSpec{Ipv4Cidr: stringPtr("10.0.0.0/24")},
	}

	data, err := Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("expected YAML output")
	}
	if !strings.Contains(string(data), "ipv4_cidr:") {
		t.Fatalf("YAML does not use proto field names:\n%s", data)
	}

	decoded := &publicv1.VirtualNetwork{}
	if err := Unmarshal(data, decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetMetadata().GetName() != "network-1" {
		t.Fatalf("metadata.name = %q, want network-1", decoded.GetMetadata().GetName())
	}
	if decoded.GetSpec().GetIpv4Cidr() != "10.0.0.0/24" {
		t.Fatalf("spec.ipv4_cidr = %q, want 10.0.0.0/24", decoded.GetSpec().GetIpv4Cidr())
	}
}

func TestMarshalTemplateIncludesEditableStructure(t *testing.T) {
	data, err := MarshalTemplate(&publicv1.VirtualNetwork{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, field := range []string{"metadata:", "name:", "spec:", "# ipv4_cidr:", "# ipv6_cidr:"} {
		if !strings.Contains(text, field) {
			t.Fatalf("template does not contain %q:\n%s", field, text)
		}
	}
	if err := Unmarshal(data, &publicv1.VirtualNetwork{}); err != nil {
		t.Fatalf("template cannot be decoded: %v\n%s", err, data)
	}
}

func TestRedactSensitiveFields(t *testing.T) {
	data := RedactSensitive([]byte("metadata:\n  name: demo\n  token: abc\ndata:\n  api_key: encoded\nspec:\n  password: secret\n"))
	text := string(data)
	if strings.Contains(text, "abc") || strings.Contains(text, "secret") {
		t.Fatalf("sensitive values were not redacted:\n%s", text)
	}
	if !strings.Contains(text, `token: "REDACTED"`) {
		t.Fatalf("redacted token missing:\n%s", text)
	}
	if !strings.Contains(text, `api_key: "REDACTED"`) {
		t.Fatalf("redacted data missing:\n%s", text)
	}
}

func stringPtr(value string) *string { return &value }
