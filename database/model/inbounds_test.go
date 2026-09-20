package model

import (
	"encoding/json"
	"testing"
)

func TestTUICMarshalJSONAddsDefaultH3ALPN(t *testing.T) {
	inbound := Inbound{
		Type:    "tuic",
		Tag:     "tuic-test",
		Options: json.RawMessage(`{"listen":"::","listen_port":443}`),
		Tls: &Tls{
			Server: json.RawMessage(`{"enabled":true}`),
		},
	}

	raw, err := inbound.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal TUIC inbound: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("unmarshal TUIC config: %v", err)
	}
	tlsConfig, ok := config["tls"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing TLS config in %s", raw)
	}
	alpn, ok := tlsConfig["alpn"].([]interface{})
	if !ok || len(alpn) != 1 || alpn[0] != "h3" {
		t.Fatalf("unexpected TUIC ALPN %#v", tlsConfig["alpn"])
	}
}

func TestTUICMarshalJSONPreservesExplicitALPN(t *testing.T) {
	inbound := Inbound{
		Type:    "tuic",
		Tag:     "tuic-test",
		Options: json.RawMessage(`{"listen":"::","listen_port":443}`),
		Tls: &Tls{
			Server: json.RawMessage(`{"enabled":true,"alpn":["custom-tuic"]}`),
		},
	}

	raw, err := inbound.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal TUIC inbound: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("unmarshal TUIC config: %v", err)
	}
	tlsConfig := config["tls"].(map[string]interface{})
	alpn := tlsConfig["alpn"].([]interface{})
	if len(alpn) != 1 || alpn[0] != "custom-tuic" {
		t.Fatalf("explicit TUIC ALPN was replaced: %#v", alpn)
	}
}
