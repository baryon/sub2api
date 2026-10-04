//go:build unit

package config

import (
	"strings"
	"testing"
)

func TestOtohaConfigDefaultsToOff(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Otoha.GroupID != 0 || cfg.Otoha.GatewayBaseURL != "" || cfg.Otoha.DefaultModel != "" {
		t.Fatalf("Otoha config should be empty by default, got %+v", cfg.Otoha)
	}
	if cfg.Otoha.Enabled() {
		t.Fatalf("Otoha should be off without a group")
	}
}

func TestOtohaConfigReadsEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("OTOHA_GROUP_ID", "42")
	t.Setenv("OTOHA_GATEWAY_BASE_URL", "https://api.otohaai.com/")
	t.Setenv("OTOHA_DEFAULT_MODEL", "gpt-6-luna")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Otoha.GroupID != 42 || cfg.Otoha.DefaultModel != "gpt-6-luna" {
		t.Fatalf("unexpected Otoha config %+v", cfg.Otoha)
	}
	if cfg.Otoha.GatewayBaseURL != "https://api.otohaai.com" {
		t.Fatalf("gateway base URL should lose its trailing slash, got %q", cfg.Otoha.GatewayBaseURL)
	}
	if !cfg.Otoha.Enabled() {
		t.Fatalf("Otoha should be on with a group and a gateway address")
	}
}

func TestOtohaConfigValidation(t *testing.T) {
	cases := []struct {
		name    string
		groupID int64
		baseURL string
		wantErr string
	}{
		{name: "off", groupID: 0, baseURL: ""},
		{name: "group without gateway", groupID: 3, baseURL: "", wantErr: "otoha.gateway_base_url"},
		{name: "negative group", groupID: -1, baseURL: "https://api.example.com", wantErr: "otoha.group_id"},
		{name: "relative gateway", groupID: 3, baseURL: "/v1", wantErr: "otoha.gateway_base_url"},
		{name: "gateway with path", groupID: 3, baseURL: "https://api.example.com/v1", wantErr: "otoha.gateway_base_url"},
		{name: "gateway with query", groupID: 3, baseURL: "https://api.example.com?x=1", wantErr: "otoha.gateway_base_url"},
		{name: "valid", groupID: 3, baseURL: "https://api.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			cfg.Otoha.GroupID = tc.groupID
			cfg.Otoha.GatewayBaseURL = tc.baseURL
			err = cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

func TestOtohaPortalDefaultsToOff(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Otoha.Portal || cfg.Otoha.PortalEnabled() {
		t.Fatalf("the Otoha portal should be off by default, got %+v", cfg.Otoha)
	}
}

func TestOtohaPortalReadsEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("OTOHA_GROUP_ID", "2")
	t.Setenv("OTOHA_GATEWAY_BASE_URL", "https://api.otohaai.com")
	t.Setenv("OTOHA_PORTAL", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Otoha.Portal || !cfg.Otoha.PortalEnabled() {
		t.Fatalf("OTOHA_PORTAL=true should turn the portal on, got %+v", cfg.Otoha)
	}
}

func TestOtohaPortalNeedsTheOtohaGroup(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.Otoha.Portal = true
	err = cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "otoha.portal") {
		t.Fatalf("Validate() error = %v, want the portal to require otoha.group_id", err)
	}
	if cfg.Otoha.PortalEnabled() {
		t.Fatalf("the portal must not count as on without the Otoha group")
	}

	cfg.Otoha.GroupID = 2
	cfg.Otoha.GatewayBaseURL = "https://api.otohaai.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error with the group set: %v", err)
	}
	if !cfg.Otoha.PortalEnabled() {
		t.Fatalf("the portal should be on with the group set")
	}
}
