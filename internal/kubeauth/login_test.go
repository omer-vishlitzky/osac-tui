package kubeauth

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCreateServiceAccountToken(t *testing.T) {
	var (
		gotKubeconfig string
		gotArgs       []string
	)
	token, err := createServiceAccountToken(context.Background(), "cluster.kubeconfig", "osac", "admin", func(_ context.Context, kubeconfig string, args ...string) ([]byte, error) {
		gotKubeconfig = kubeconfig
		gotArgs = args
		return []byte("service-account-jwt\n"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotKubeconfig != "cluster.kubeconfig" {
		t.Fatalf("kubeconfig = %q", gotKubeconfig)
	}
	if want := []string{"-n", "osac", "create", "token", "admin"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("kubectl args = %v, want %v", gotArgs, want)
	}
	if token != "service-account-jwt" {
		t.Fatalf("token = %q", token)
	}
}

func TestCreateServiceAccountTokenReportsKubectlFailure(t *testing.T) {
	wantErr := errors.New("forbidden")
	_, err := createServiceAccountToken(context.Background(), "", "osac", "admin", func(context.Context, string, ...string) ([]byte, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}

func TestEndpointAddressUsesStandardHTTPSPort(t *testing.T) {
	t.Setenv("OSAC_GATEWAY_PORT", "")
	got, err := endpointAddress("fulfillment-api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fulfillment-api.example.com:443" {
		t.Fatalf("address = %q", got)
	}
}

func TestEndpointAddressUsesKindGatewayPortForLocalhost(t *testing.T) {
	t.Setenv("OSAC_GATEWAY_PORT", "")
	got, err := endpointAddress("fulfillment-api.osac.localhost")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fulfillment-api.osac.localhost:8443" {
		t.Fatalf("address = %q", got)
	}
}

func TestEndpointAddressHonorsGatewayPortOverride(t *testing.T) {
	t.Setenv("OSAC_GATEWAY_PORT", "9443")
	got, err := endpointAddress("fulfillment-api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fulfillment-api.example.com:9443" {
		t.Fatalf("address = %q", got)
	}
}

func TestEndpointAddressRejectsInvalidGatewayPort(t *testing.T) {
	t.Setenv("OSAC_GATEWAY_PORT", "not-a-port")
	if _, err := endpointAddress("fulfillment-api.example.com"); err == nil {
		t.Fatal("expected invalid gateway port error")
	}
}

func TestParseTLSRouteHostnames(t *testing.T) {
	got, err := parseResourceEndpoints([]byte(`{"items":[{"metadata":{"name":"fulfillment-api"},"spec":{"hostnames":["fulfillment-api.osac.localhost"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != (endpoint{name: "fulfillment-api", host: "fulfillment-api.osac.localhost"}) {
		t.Fatalf("parsed TLSRoute endpoints = %#v", got)
	}
}

func TestSelectEndpointPrefersFulfillmentAPI(t *testing.T) {
	got, err := selectEndpoint([]endpoint{
		{name: "osac-ui", host: "console.osac.example.com"},
		{name: "fulfillment-api", host: "fulfillment-api.osac.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "fulfillment-api.osac.example.com" {
		t.Fatalf("selected host = %q", got)
	}
}

func TestSelectEndpointRejectsAmbiguousOSACHosts(t *testing.T) {
	_, err := selectEndpoint([]endpoint{
		{name: "osac-ui-a", host: "ui-a.osac.example.com"},
		{name: "osac-ui-b", host: "ui-b.osac.example.com"},
	})
	if err == nil {
		t.Fatal("expected ambiguous host error")
	}
}

func TestSelectEndpointNeverUsesOSACUIHost(t *testing.T) {
	_, err := selectEndpoint([]endpoint{{name: "osac-ui", host: "console.osac.example.com"}})
	if err == nil {
		t.Fatal("selected the OSAC UI host as the fulfillment API")
	}
}

func TestSelectEndpointUsesSingleUnlabeledHost(t *testing.T) {
	got, err := selectEndpoint([]endpoint{{name: "gateway", host: "api.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "api.example.com" {
		t.Fatalf("selected host = %q", got)
	}
}
