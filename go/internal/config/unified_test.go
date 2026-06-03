package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUnifiedConfig_FileMissing(t *testing.T) {
	_, err := LoadUnifiedConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadUnifiedConfig_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadUnifiedConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestLoadUnifiedConfig_Defaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "sqp_x"},
		"sonarcloud": {"token": "squ_x", "organization": "my-org"}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadUnifiedConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SonarCloud.URL != DefaultSonarCloudURL {
		t.Errorf("sonarcloud.url default not applied: got %q, want %q", cfg.SonarCloud.URL, DefaultSonarCloudURL)
	}
	if cfg.SonarCloud.EnterpriseKey != "my-org" {
		t.Errorf("sonarcloud.enterpriseKey should default to organization: got %q", cfg.SonarCloud.EnterpriseKey)
	}
	if cfg.Mode() != ModeSingleOrg {
		t.Errorf("expected single-org mode, got %s", cfg.Mode())
	}
}

func TestLoadUnifiedConfig_SingleOrgPreservesExplicitEnterpriseKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "sqp_x"},
		"sonarcloud": {"token": "squ_x", "organization": "my-org", "enterpriseKey": "my-enterprise"}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadUnifiedConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SonarCloud.EnterpriseKey != "my-enterprise" {
		t.Errorf("explicit enterpriseKey should be preserved: got %q", cfg.SonarCloud.EnterpriseKey)
	}
}

func TestLoadUnifiedConfig_MultiOrgMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "sqp_x"},
		"sonarcloud": {
			"token": "squ_enterprise",
			"organizations": [
				{"key": "org-a", "token": "squ_a"},
				{"key": "org-b", "token": "squ_b", "url": "https://sonarcloud.io/"}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadUnifiedConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode() != ModeMultiOrg {
		t.Errorf("expected multi-org mode, got %s", cfg.Mode())
	}
	if got := cfg.SonarCloud.Organizations[1].URL; got != "https://sonarcloud.io/" {
		t.Errorf("explicit per-org URL not preserved: got %q", got)
	}
}

func TestLoadUnifiedConfig_OrgURLDefaultsToTopLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "sqp_x"},
		"sonarcloud": {
			"url": "https://sonarcloud.io/",
			"organizations": [{"key": "org-a", "token": "squ_a"}]
		}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadUnifiedConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.SonarCloud.Organizations[0].URL; got != "https://sonarcloud.io/" {
		t.Errorf("per-org URL should fall back to top-level: got %q", got)
	}
}

func TestLoadUnifiedConfig_ProjectKeyPassthrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "sqp_x", "projectKey": "my-project"},
		"sonarcloud": {"token": "squ_x", "organization": "my-org"}
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg, err := LoadUnifiedConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SonarQube.ProjectKey != "my-project" {
		t.Errorf("projectKey not preserved: got %q", cfg.SonarQube.ProjectKey)
	}
}

func TestLoadUnifiedConfig_EmptyPath(t *testing.T) {
	_, err := LoadUnifiedConfig("")
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestUnifiedConfig_ValidateMigrate_SingleOrg_OK(t *testing.T) {
	cfg := UnifiedConfig{
		SonarQube:  SonarQubeConfig{URL: "https://sq.example.com", Token: "sqp_x"},
		SonarCloud: SonarCloudConfig{Token: "squ_x", Organization: "my-org"},
	}
	if err := cfg.ValidateMigrate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnifiedConfig_ValidateMigrate_MultiOrg_OK(t *testing.T) {
	cfg := UnifiedConfig{
		SonarQube: SonarQubeConfig{URL: "https://sq.example.com", Token: "sqp_x"},
		SonarCloud: SonarCloudConfig{
			Organizations: []OrganizationConfig{
				{Key: "org-a", Token: "squ_a"},
				{Key: "org-b", Token: "squ_b"},
			},
		},
	}
	if err := cfg.ValidateMigrate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnifiedConfig_ValidateMigrate_MissingSQURL(t *testing.T) {
	cfg := UnifiedConfig{
		SonarQube:  SonarQubeConfig{Token: "sqp_x"},
		SonarCloud: SonarCloudConfig{Token: "squ_x", Organization: "my-org"},
	}
	err := cfg.ValidateMigrate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var reqErr *MigrateRequirementError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected *MigrateRequirementError, got %T", err)
	}
	if reqErr.Field != "sonarqube.url" {
		t.Errorf("expected sonarqube.url, got %q", reqErr.Field)
	}
}

func TestUnifiedConfig_ValidateMigrate_SingleOrgMissingOrg(t *testing.T) {
	cfg := UnifiedConfig{
		SonarQube:  SonarQubeConfig{URL: "https://sq.example.com", Token: "sqp_x"},
		SonarCloud: SonarCloudConfig{Token: "squ_x"},
	}
	err := cfg.ValidateMigrate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var reqErr *MigrateRequirementError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected *MigrateRequirementError, got %T", err)
	}
	if reqErr.Field != "sonarcloud.organization" {
		t.Errorf("expected sonarcloud.organization, got %q", reqErr.Field)
	}
}

func TestUnifiedConfig_ValidateMigrate_MultiOrgMissingPerOrgToken(t *testing.T) {
	cfg := UnifiedConfig{
		SonarQube: SonarQubeConfig{URL: "https://sq.example.com", Token: "sqp_x"},
		SonarCloud: SonarCloudConfig{
			Organizations: []OrganizationConfig{
				{Key: "org-a", Token: "squ_a"},
				{Key: "org-b"}, // missing token
			},
		},
	}
	err := cfg.ValidateMigrate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var reqErr *MigrateRequirementError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected *MigrateRequirementError, got %T", err)
	}
	if reqErr.Field != "sonarcloud.organizations[1].token" {
		t.Errorf("expected sonarcloud.organizations[1].token, got %q", reqErr.Field)
	}
}

func TestMode_String(t *testing.T) {
	if got := ModeSingleOrg.String(); got != "single-org" {
		t.Errorf("ModeSingleOrg.String() = %q, want %q", got, "single-org")
	}
	if got := ModeMultiOrg.String(); got != "multi-org" {
		t.Errorf("ModeMultiOrg.String() = %q, want %q", got, "multi-org")
	}
}
