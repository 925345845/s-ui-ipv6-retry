package service

import "testing"

func TestIndependentRuntimeDefaults(t *testing.T) {
	if defaultValueMap["webPort"] != "2195" || defaultValueMap["subPort"] != "2196" {
		t.Fatal("independent instance must not use original panel ports")
	}
	if defaultLocalAgentBinary != "/usr/local/s-ui-ipv6-retry/sui-agent" || defaultLocalControlSocket != "/run/s-ui-ipv6-retry/control.sock" || localAgentServiceName != "s-ui-ipv6-retry-agent.service" {
		t.Fatal("agent must not control the original instance")
	}
}
