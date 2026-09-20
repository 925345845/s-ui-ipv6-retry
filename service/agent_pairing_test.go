package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAgentPairingLink(t *testing.T) {
	code := strings.Repeat("a", 43)
	endpoint, parsedCode, err := parseAgentPairingLink("https://panel.example/app/agent/v1/pair#" + code)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://panel.example/app/agent/v1/pair" || parsedCode != code {
		t.Fatalf("unexpected parsed pairing link: endpoint=%q code=%q", endpoint, parsedCode)
	}
	enrollmentEndpoint, enrollmentCode, err := parseAgentPairingLink("https://panel.example/app/agent/v1/enroll#" + code)
	if err != nil {
		t.Fatal(err)
	}
	if enrollmentEndpoint != "https://panel.example/app/agent/v1/enroll" || enrollmentCode != code {
		t.Fatalf("unexpected parsed enrollment API: endpoint=%q code=%q", enrollmentEndpoint, enrollmentCode)
	}

	invalid := []string{
		"",
		"ftp://panel.example/app/agent/v1/pair#" + code,
		"https://user@panel.example/app/agent/v1/pair#" + code,
		"https://panel.example/app/api/agents#" + code,
		"https://panel.example/app/agent/v1/pair#short",
	}
	for _, value := range invalid {
		if _, _, err := parseAgentPairingLink(value); err == nil {
			t.Fatalf("accepted invalid pairing link %q", value)
		}
	}
}

func TestExchangeAgentPairingPostsFragmentCode(t *testing.T) {
	code := strings.Repeat("b", 43)
	token := strings.Repeat("c", 43)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/agent/v1/pair" {
			t.Errorf("unexpected pairing request: %s %s", r.Method, r.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.URL.Fragment != "" || r.URL.RawQuery != "" {
			t.Errorf("pairing secret leaked into request URL: %s", r.URL.String())
			http.Error(w, "secret leaked", http.StatusBadRequest)
			return
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		if payload["code"] != code {
			t.Errorf("unexpected pairing code: %q", payload["code"])
			http.Error(w, "invalid code", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(payload["name"]) == "" {
			t.Error("managed server hostname was not included")
			http.Error(w, "missing name", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"obj": map[string]string{
				"panel_url": serverURLFromRequest(r) + "/app/",
				"token":     token,
				"version":   "test",
			},
		})
	}))
	defer server.Close()

	connection, pairedToken, err := exchangeAgentPairing(
		context.Background(), server.URL+"/app/agent/v1/pair#"+code, false, server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if connection.PanelURL != server.URL+"/app/" || connection.Version != "test" || pairedToken != token {
		t.Fatalf("unexpected pairing result: connection=%#v token=%q", connection, pairedToken)
	}
}

func serverURLFromRequest(r *http.Request) string {
	return "http://" + r.Host
}
