// Package config holds the shared, command-agnostic configuration types and
// loaders used across the sonar-migration-tool CLI.
//
// The minimum-scope UnifiedConfig defined here mirrors the shape sketched in
// roadmap/specs/SPEC-025-configuration-validation.md (sonarqube + sonarcloud
// blocks, with multi-org support via the organizations array). Validation
// tags, env-var overrides, and the validate / test commands are intentionally
// deferred — see the spec for the full target behaviour.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// DefaultSonarCloudURL is the public SonarQube Cloud endpoint used when
// sonarcloud.url is left empty in the config file or on the CLI.
const DefaultSonarCloudURL = "https://sonarcloud.io/"

// UnifiedConfig is the top-level configuration struct consumed by commands
// that need both SonarQube Server (source) and SonarQube Cloud (target)
// credentials. Each command decides which subset of fields it actually uses.
type UnifiedConfig struct {
	SonarQube  SonarQubeConfig  `json:"sonarqube"`
	SonarCloud SonarCloudConfig `json:"sonarcloud"`
}

// SonarQubeConfig holds the SonarQube Server (source) side of a migration.
type SonarQubeConfig struct {
	URL        string `json:"url"`
	Token      string `json:"token"`
	ProjectKey string `json:"projectKey,omitempty"` // optional: scope to one project
}

// SonarCloudConfig holds the SonarQube Cloud (target) side of a migration.
// Supports both single-org (Token + Organization) and multi-org
// (Organizations) modes — see MigrateMode for the rule.
type SonarCloudConfig struct {
	URL           string               `json:"url"`
	Token         string               `json:"token,omitempty"`
	Organization  string               `json:"organization,omitempty"`
	EnterpriseKey string               `json:"enterpriseKey,omitempty"`
	Organizations []OrganizationConfig `json:"organizations,omitempty"`
}

// OrganizationConfig describes one target SonarQube Cloud organization in
// multi-org mode. The URL field is optional and falls back to the top-level
// SonarCloudConfig.URL when empty.
type OrganizationConfig struct {
	Key   string `json:"key"`
	Token string `json:"token"`
	URL   string `json:"url,omitempty"`
}

// Mode reports whether the configuration is for a single-org or multi-org
// migration. Multi-org is signalled by the presence of at least one entry in
// SonarCloudConfig.Organizations. The top-level Token (if set) is then
// treated as the enterprise token; per-org tokens win when supplied.
func (c UnifiedConfig) Mode() Mode {
	if len(c.SonarCloud.Organizations) > 0 {
		return ModeMultiOrg
	}
	return ModeSingleOrg
}

// Mode enumerates the two ways the unified config can target SonarQube Cloud.
type Mode int

const (
	// ModeSingleOrg is the default: one token + one organization, optional
	// enterprise key.
	ModeSingleOrg Mode = iota
	// ModeMultiOrg routes per-project traffic through the organizations
	// array; the top-level token (if set) acts as the enterprise token.
	ModeMultiOrg
)

// String renders the mode for log output and error messages.
func (m Mode) String() string {
	switch m {
	case ModeMultiOrg:
		return "multi-org"
	default:
		return "single-org"
	}
}

// LoadUnifiedConfig reads path, parses it as JSON into a UnifiedConfig, and
// applies the default values documented on the struct fields. It does not
// validate that required fields are set — callers that care should use
// Validate. The zero value is returned for an empty path so callers can
// layer the loader on top of flag-based config without a conditional.
func LoadUnifiedConfig(path string) (UnifiedConfig, error) {
	var cfg UnifiedConfig
	if path == "" {
		return cfg, errors.New("config: empty path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("config: read %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %q: %w", path, err)
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

// ApplyDefaults fills in the documented default values for fields the user
// left empty. Idempotent. Callers that mutate a UnifiedConfig in place
// (e.g. the migrate command after merging CLI flags on top of a loaded
// config) should invoke this once before validation.
func (c *UnifiedConfig) ApplyDefaults() {
	if c.SonarCloud.URL == "" {
		c.SonarCloud.URL = DefaultSonarCloudURL
	}
	if c.Mode() == ModeSingleOrg && c.SonarCloud.EnterpriseKey == "" {
		c.SonarCloud.EnterpriseKey = c.SonarCloud.Organization
	}
	for i := range c.SonarCloud.Organizations {
		if c.SonarCloud.Organizations[i].URL == "" {
			c.SonarCloud.Organizations[i].URL = c.SonarCloud.URL
		}
	}
}

// MigrateRequirementError is returned by Validate when a required field is
// missing for the chosen mode. The Message is already user-friendly; callers
// can print it directly.
type MigrateRequirementError struct {
	Field   string
	Message string
}

func (e *MigrateRequirementError) Error() string { return e.Message }

// ValidateMigrate checks that the unified config has enough information to
// run a migration. It returns nil on success, or a *MigrateRequirementError
// describing the first missing field otherwise. The check is mode-aware:
// multi-org mode requires at least one entry in Organizations; single-org
// mode requires Token + Organization.
func (c UnifiedConfig) ValidateMigrate() error {
	if err := c.validateSonarQube(); err != nil {
		return err
	}
	if c.Mode() == ModeMultiOrg {
		return c.validateMultiOrg()
	}
	return c.validateSingleOrg()
}

func (c UnifiedConfig) validateSonarQube() error {
	if c.SonarQube.URL == "" {
		return &MigrateRequirementError{Field: "sonarqube.url", Message: "sonarqube.url is required"}
	}
	if c.SonarQube.Token == "" {
		return &MigrateRequirementError{Field: "sonarqube.token", Message: "sonarqube.token is required"}
	}
	return nil
}

func (c UnifiedConfig) validateSingleOrg() error {
	if c.SonarCloud.Token == "" {
		return &MigrateRequirementError{Field: "sonarcloud.token", Message: "sonarcloud.token is required"}
	}
	if c.SonarCloud.Organization == "" {
		return &MigrateRequirementError{
			Field:   "sonarcloud.organization",
			Message: "sonarcloud.organization is required (or set sonarcloud.organizations for multi-org mode)",
		}
	}
	return nil
}

func (c UnifiedConfig) validateMultiOrg() error {
	if len(c.SonarCloud.Organizations) == 0 {
		return &MigrateRequirementError{
			Field:   "sonarcloud.organizations",
			Message: "sonarcloud.organizations must contain at least one entry in multi-org mode",
		}
	}
	for i, org := range c.SonarCloud.Organizations {
		if org.Key == "" {
			return &MigrateRequirementError{
				Field:   fmt.Sprintf("sonarcloud.organizations[%d].key", i),
				Message: fmt.Sprintf("sonarcloud.organizations[%d].key is required", i),
			}
		}
		if org.Token == "" {
			return &MigrateRequirementError{
				Field:   fmt.Sprintf("sonarcloud.organizations[%d].token", i),
				Message: fmt.Sprintf("sonarcloud.organizations[%d].token is required", i),
			}
		}
	}
	return nil
}
