package client

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestConnectionInfoFromToken(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"preferred_username": "tenant1_user",
		"organization": map[string]any{
			"tenant1": map[string]any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"

	got := connectionInfo("fulfillment-api.osac.localhost:8443", token)
	if got.Address != "fulfillment-api.osac.localhost:8443" {
		t.Errorf("address = %q", got.Address)
	}
	if got.User != "tenant1_user" {
		t.Errorf("user = %q", got.User)
	}
	if got.Organization != "tenant1" {
		t.Errorf("organization = %q", got.Organization)
	}
}
