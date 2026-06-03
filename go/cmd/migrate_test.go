package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// newMigrateTestCmd declares the full set of flags the real migrateCmd
// registers in init(). Tests use it to exercise resolveMigrateConfig
// without dragging in the rest of the command tree.
func newMigrateTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "migrate"}
	f := cmd.Flags()
	f.String("config", "", "")
	f.String("sq-url", "", "")
	f.String("sq-token", "", "")
	f.String("sq-project-key", "", "")
	f.String("sc-url", "", "")
	f.String("sc-token", "", "")
	f.String("sc-org", "", "")
	f.String("sc-enterprise-key", "", "")
	f.String("edition", "", "")
	f.String("url", "", "")
	f.String("run_id", "", "")
	f.Int("concurrency", 0, "")
	f.String("export_directory", "", "")
	f.String("target_task", "", "")
	f.Bool("skip_profiles", false, "")
	f.Bool("include_scan_history", false, "")
	f.Bool("debug", false, "")
	f.String("default_organization", "", "")
	return cmd
}

func writeMigrateConfig(t *testing.T, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// CLI --sq-token flag overrides the value from the config file.
func TestResolveMigrateConfig_SQToken_CLIOverridesConfig(t *testing.T) {
	path := writeMigrateConfig(t, `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "from-config"},
		"sonarcloud": {"token": "squ_x", "organization": "my-org"}
	}`)
	cmd := newMigrateTestCmd()
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("sq-token", "from-cli")

	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarQube.Token != "from-cli" {
		t.Errorf("CLI flag should override config, got %q", cfg.SonarQube.Token)
	}
}

// Config file value is used when --sq-token is absent.
func TestResolveMigrateConfig_SQToken_ConfigOnly(t *testing.T) {
	path := writeMigrateConfig(t, `{
		"sonarqube":  {"url": "https://sq.example.com", "token": "from-config"},
		"sonarcloud": {"token": "squ_x", "organization": "my-org"}
	}`)
	cmd := newMigrateTestCmd()
	_ = cmd.Flags().Set("config", path)

	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarQube.Token != "from-config" {
		t.Errorf("expected config value, got %q", cfg.SonarQube.Token)
	}
}

// Neither config nor CLI → empty (validation will fail downstream).
func TestResolveMigrateConfig_SQToken_Unset(t *testing.T) {
	cmd := newMigrateTestCmd()
	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarQube.Token != "" {
		t.Errorf("expected empty, got %q", cfg.SonarQube.Token)
	}
}

// --url (legacy alias) populates sc-url when sc-url is not set.
func TestResolveMigrateConfig_LegacyURLAlias(t *testing.T) {
	cmd := newMigrateTestCmd()
	_ = cmd.Flags().Set("url", "https://legacy.example.com/")

	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarCloud.URL != "https://legacy.example.com/" {
		t.Errorf("expected legacy --url to populate sc-url, got %q", cfg.SonarCloud.URL)
	}
}

// --sc-url takes precedence over --url when both are set.
func TestResolveMigrateConfig_SCURLBeatsLegacy(t *testing.T) {
	cmd := newMigrateTestCmd()
	_ = cmd.Flags().Set("url", "https://legacy.example.com/")
	_ = cmd.Flags().Set("sc-url", "https://sc.example.com/")

	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarCloud.URL != "https://sc.example.com/" {
		t.Errorf("expected --sc-url to win, got %q", cfg.SonarCloud.URL)
	}
}

// ApplyDefaults populates SonarCloud.URL with the public default when
// nothing in the config or on the CLI sets it.
func TestResolveMigrateConfig_DefaultsApplied(t *testing.T) {
	cmd := newMigrateTestCmd()
	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		t.Fatalf("resolveMigrateConfig: %v", err)
	}
	if cfg.SonarCloud.URL == "" {
		t.Errorf("expected default SonarCloud URL, got empty string")
	}
}
