package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonar-solutions/sonar-migration-tool/internal/config"
	"github.com/sonar-solutions/sonar-migration-tool/internal/report/summary"
	"github.com/spf13/cobra"
)

// Flag names for the migrate command. Most of these are shared with the
// transfer / extract / structure commands and live as constants at the
// top of transfer.go; the migrate-specific ones (and the few that differ
// from transfer's naming) are declared here.
const (
	flagSQProjectKey    = "sq-project-key"
	flagURL             = "url" // legacy --url alias for --sc-url
	flagEdition         = "edition"
	flagRunID           = "run_id"
	flagTargetTask      = "target_task"
	flagExportDirectory = "export_directory" // distinct from transfer's --export-dir
	flagDefaultOrg      = "default_organization"
	flagSkipProfiles    = "skip_profiles"
)

// migrateCmd is the single-command entry point for a full
// SonarQube Server -> SonarQube Cloud migration. It chains the four phases
// (extract, structure, mappings, migrate) internally, mirroring the
// behaviour of the transfer command so the user only has to run one
// command. Configuration is read from a JSON file (--config) using the
// unified config shape, or supplied via the explicit --sq-* / --sc-*
// flags below.
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate SonarQube Server configuration to SonarQube Cloud in one command",
	Long: `Migrate SonarQube Server configuration to SonarQube Cloud.

The command runs the full pipeline in a single invocation:
  [1/4] Extract     — pull configuration from SonarQube Server
  [2/4] Structure   — group projects into organizations (writes organizations.csv)
  [3/4] Mappings    — generate the per-entity mapping CSVs
  [4/4] Migrate     — apply the configuration to SonarQube Cloud

After the Structure phase, organizations.csv is left for the operator to
fill in the target SonarQube Cloud organization key for each row. Re-run
migrate with the same --export_directory to continue from the Mappings
phase. See docs/MIGRATE.md for the full workflow.

Configuration is supplied via a unified config file (see SPEC-025):

  {
    "sonarqube":  { "url": "...", "token": "...", "projectKey": "..." },
    "sonarcloud": {
      "url": "https://sonarcloud.io/",
      "token": "...",
      "organization": "...",
      "enterpriseKey": "...",
      "organizations": [ { "key": "...", "token": "..." } ]
    }
  }

Or via the equivalent --sq-* and --sc-* CLI flags.`,
	Args: cobra.NoArgs,
	RunE: runMigrate,
}

func init() {
	f := migrateCmd.Flags()
	f.String(flagConfig, "", "Path to a unified JSON configuration file")
	f.String(flagSQURL, "", "SonarQube Server URL")
	f.String(flagSQToken, "", "SonarQube Server token")
	f.String(flagSQProjectKey, "", "Scope extraction to a single SonarQube Server project key")
	f.String(flagSCURL, "", "SonarQube Cloud URL (default: https://sonarcloud.io/)")
	f.String(flagSCToken, "", "SonarQube Cloud token (single-org mode)")
	f.String(flagSCOrg, "", "SonarQube Cloud organization key (single-org mode)")
	f.String(flagSCEnterpriseKey, "", "SonarQube Cloud enterprise key (defaults to --sc-org)")

	// Existing flags kept as overrides / advanced options.
	f.String(flagEdition, "", "SonarQube Cloud license edition")
	f.String(flagURL, "", "Alias for --sc-url (legacy)")
	f.String(flagRunID, "", "Resume an in-progress migration by run ID")
	f.String(flagTargetTask, "", "Run a specific migration task (with its dependencies)")
	f.String(flagExportDirectory, "", "Working directory for intermediate files (default: ./migration-files)")
	f.String(flagDefaultOrg, "", "SonarQube Cloud organization applied to every project when organizations.csv has no mapping")
	f.Int(flagConcurrency, 0, "Max concurrent requests")
	f.Bool(flagSkipProfiles, false, "Skip quality profile migration/provisioning")
	f.Bool(flagIncludeScanHistory, false, "Extract and import full issue/hotspot scan history")
}

// runMigrate is the RunE for the migrate command. It loads the unified
// config, validates it, and chains the four phases via the helpers in
// migrate_phases.go.
func runMigrate(cmd *cobra.Command, _ []string) error {
	cfg, err := resolveMigrateConfig(cmd)
	if err != nil {
		return err
	}
	if err := cfg.ValidateMigrate(); err != nil {
		return err
	}
	mc := migrateCmdConfigFrom(cfg)
	mc.exportDir = resolveExportDir(cmd)
	applyAdvancedFlags(&mc, cmd)

	ctx := cmd.Context()

	// Phase 1: extract
	skipped, err := runMigrateExtract(ctx, mc)
	if err != nil {
		return err
	}
	warnSkippedProjects(skipped)

	// Phase 2: structure
	if err := runMigrateStructure(mc); err != nil {
		return err
	}

	// Phase 3: only proceed if the operator has filled in the org mapping.
	// On the first run the CSV will have empty sonarcloud_org_key values;
	// we stop here and tell the user what to do next.
	if err := requireOrgMapping(mc.exportDir); err != nil {
		printMappingPrompt(mc.exportDir)
		return err
	}

	// Phase 3: mappings
	if err := runMigrateMappings(mc); err != nil {
		return err
	}

	// Phase 4: migrate
	runID, err := runMigrateMigrate(ctx, mc)
	if err != nil {
		return err
	}
	emitMigratePDFReport(mc.exportDir, runID)
	return nil
}

// resolveMigrateConfig merges the unified config file (if any) with the
// CLI flags, returning a fully-populated config. Precedence: config file
// is the base; every explicitly-set CLI flag wins.
func resolveMigrateConfig(cmd *cobra.Command) (config.UnifiedConfig, error) {
	var cfg config.UnifiedConfig
	configFile, _ := cmd.Flags().GetString(flagConfig)
	if configFile != "" {
		loaded, err := config.LoadUnifiedConfig(configFile)
		if err != nil {
			return cfg, err
		}
		cfg = loaded
	}
	overrideString(cmd, flagSQURL, &cfg.SonarQube.URL)
	overrideString(cmd, flagSQToken, &cfg.SonarQube.Token)
	overrideString(cmd, flagSQProjectKey, &cfg.SonarQube.ProjectKey)
	overrideString(cmd, flagSCURL, &cfg.SonarCloud.URL)
	overrideString(cmd, flagSCToken, &cfg.SonarCloud.Token)
	overrideString(cmd, flagSCOrg, &cfg.SonarCloud.Organization)
	overrideString(cmd, flagSCEnterpriseKey, &cfg.SonarCloud.EnterpriseKey)
	// Legacy --url aliases --sc-url for backward compat with older scripts.
	if !cmd.Flags().Changed(flagSCURL) {
		overrideString(cmd, flagURL, &cfg.SonarCloud.URL)
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

// resolveExportDir returns the effective export directory: the CLI flag
// wins, otherwise the value baked into the unified config, otherwise the
// package-level default.
func resolveExportDir(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString(flagExportDirectory); v != "" {
		return v
	}
	return DefaultExportDirectory
}

// applyAdvancedFlags copies the remaining (non-unified-config) flag values
// onto the migrateCmdConfig. These are the flags that are not part of the
// unified config shape (edition, run_id, target_task, etc.) so they live
// outside resolveMigrateConfig.
func applyAdvancedFlags(mc *migrateCmdConfig, cmd *cobra.Command) {
	if v, _ := cmd.Flags().GetString(flagEdition); v != "" {
		mc.edition = v
	}
	if v, _ := cmd.Flags().GetString(flagRunID); v != "" {
		mc.runID = v
	}
	if v, _ := cmd.Flags().GetString(flagTargetTask); v != "" {
		mc.targetTask = v
	}
	if v, _ := cmd.Flags().GetString(flagDefaultOrg); v != "" {
		mc.defaultOrg = v
	}
	if v, _ := cmd.Flags().GetInt(flagConcurrency); v != 0 {
		mc.concurrency = v
	}
	if cmd.Flags().Changed(flagSkipProfiles) {
		mc.skipProfiles, _ = cmd.Flags().GetBool(flagSkipProfiles)
	}
	if cmd.Flags().Changed(flagIncludeScanHistory) {
		mc.includeScanHistory, _ = cmd.Flags().GetBool(flagIncludeScanHistory)
	}
	if cmd.Flags().Changed("debug") {
		mc.debug, _ = cmd.Flags().GetBool("debug")
	}
}

// migrateCmdConfigFrom flattens a UnifiedConfig into the internal
// migrateCmdConfig used by the phase helpers. Only the fields the new
// command path consumes are populated; multi-org organizations are
// preserved for future use.
func migrateCmdConfigFrom(cfg config.UnifiedConfig) migrateCmdConfig {
	mc := migrateCmdConfig{
		sqURL:           cfg.SonarQube.URL,
		sqToken:         cfg.SonarQube.Token,
		sqProjectKey:    cfg.SonarQube.ProjectKey,
		scURL:           cfg.SonarCloud.URL,
		scToken:         cfg.SonarCloud.Token,
		scOrg:           cfg.SonarCloud.Organization,
		scEnterpriseKey: cfg.SonarCloud.EnterpriseKey,
	}
	for _, org := range cfg.SonarCloud.Organizations {
		mc.scOrganizations = append(mc.scOrganizations, orgCredentials{
			Key:   org.Key,
			Token: org.Token,
			URL:   org.URL,
		})
	}
	return mc
}

// emitMigratePDFReport writes the migration summary PDF for the run, on a
// best-effort basis. Failures here are non-fatal — the migration itself
// already succeeded.
func emitMigratePDFReport(exportDir, runID string) {
	if runID == "" {
		return
	}
	runDir := filepath.Join(exportDir, runID)
	pdfPath, pdfErr := summary.GeneratePDFReport(runDir, exportDir, exportDir)
	if pdfErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not generate PDF summary report: %v\n", pdfErr)
		return
	}
	fmt.Printf("PDF summary report: %s\n", pdfPath)
}
