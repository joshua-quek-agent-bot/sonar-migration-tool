package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sonar-solutions/sonar-migration-tool/internal/extract"
	"github.com/sonar-solutions/sonar-migration-tool/internal/migrate"
	"github.com/sonar-solutions/sonar-migration-tool/internal/structure"
)

// migrateCmdConfig is the resolved configuration for the migrate command
// after merging file and flag values. It is internal because every field is
// consumed by one of the phase helpers below.
type migrateCmdConfig struct {
	sqURL              string
	sqToken            string
	sqProjectKey       string
	scURL              string
	scToken            string
	scOrg              string
	scEnterpriseKey    string
	scOrganizations    []orgCredentials // populated only in multi-org mode
	exportDir          string
	edition            string
	runID              string
	targetTask         string
	concurrency        int
	skipProfiles       bool
	includeScanHistory bool
	debug              bool
	defaultOrg         string
}

// orgCredentials captures the per-org target fields used by multi-org
// migrations.
type orgCredentials struct {
	Key   string
	Token string
	URL   string
}

// runMigrateExtract connects to SonarQube Server and pulls the project
// configuration. It returns the list of project keys the extract phase
// skipped due to insufficient privileges, so the caller can surface them
// via warnSkippedProjects.
func runMigrateExtract(ctx context.Context, cfg migrateCmdConfig) ([]string, error) {
	printPhase(1, 4, "Extracting from SonarQube Server...")
	var projectKeys []string
	if cfg.sqProjectKey != "" {
		projectKeys = []string{cfg.sqProjectKey}
	}
	skipped, err := extract.RunExtract(ctx, extract.ExtractConfig{
		URL:                cfg.sqURL,
		Token:              cfg.sqToken,
		ExportDirectory:    cfg.exportDir,
		ProjectKeys:        projectKeys,
		Concurrency:        cfg.concurrency,
		IncludeScanHistory: cfg.includeScanHistory,
	})
	if err != nil {
		return nil, fmt.Errorf("extract failed: %w", err)
	}
	return skipped, nil
}

// runMigrateStructure groups the extracted projects into organizations and
// writes organizations.csv and projects.csv. Unlike transfer, this does
// NOT pre-populate the sonarcloud_org_key column — the user must fill it
// in before re-running migrate (the new command pauses with a clear message
// if any row is empty).
func runMigrateStructure(cfg migrateCmdConfig) error {
	printPhase(2, 4, "Building organization structure...")
	if err := structure.RunStructure(cfg.exportDir); err != nil {
		return fmt.Errorf("structure failed: %w", err)
	}
	return nil
}

// runMigrateMappings generates the per-entity mapping CSVs (gates, profiles,
// groups, templates, portfolios) that the migrate phase reads.
func runMigrateMappings(cfg migrateCmdConfig) error {
	printPhase(3, 4, "Generating entity mappings...")
	if err := structure.RunMappings(cfg.exportDir); err != nil {
		return fmt.Errorf("mappings failed: %w", err)
	}
	return nil
}

// runMigrateMigrate pushes the configuration into SonarQube Cloud and
// returns the run ID, which the caller uses to locate the request log and
// PDF report directory.
func runMigrateMigrate(ctx context.Context, cfg migrateCmdConfig) (string, error) {
	printPhase(4, 4, "Migrating to SonarQube Cloud...")
	runID, err := migrate.RunMigrate(ctx, migrate.MigrateConfig{
		Token:               cfg.scToken,
		EnterpriseKey:       cfg.scEnterpriseKey,
		Edition:             cfg.edition,
		URL:                 cfg.scURL,
		RunID:               cfg.runID,
		Concurrency:         cfg.concurrency,
		ExportDirectory:     cfg.exportDir,
		TargetTask:          cfg.targetTask,
		SkipProfiles:        cfg.skipProfiles,
		IncludeScanHistory:  cfg.includeScanHistory,
		Debug:               cfg.debug,
		DefaultOrganization: cfg.defaultOrg,
	})
	if err != nil {
		return "", fmt.Errorf("migrate failed: %w", err)
	}
	return runID, nil
}

// orgMappingComplete reports whether every row in organizations.csv has a
// non-empty sonarcloud_org_key. Used by the migrate command to decide
// whether to proceed with mappings or stop and ask the user to edit the
// CSV first.
func orgMappingComplete(exportDir string) (bool, int, error) {
	rows, err := structure.LoadCSV(exportDir, "organizations.csv")
	if err != nil {
		// If the file is missing, structure hasn't run yet — treat as
		// incomplete so the caller surfaces a helpful error.
		return false, 0, fmt.Errorf("loading organizations.csv: %w", err)
	}
	if len(rows) == 0 {
		return false, 0, nil
	}
	missing := 0
	for _, row := range rows {
		key, _ := row["sonarcloud_org_key"].(string)
		if key == "" {
			missing++
		}
	}
	return missing == 0, missing, nil
}

// requireOrgMapping pauses the migration with a clear message if any row
// in organizations.csv is missing a sonarcloud_org_key. Returns nil when
// the mapping is complete; returns a non-nil error suitable for surfacing
// directly to the user otherwise.
func requireOrgMapping(exportDir string) error {
	complete, missing, err := orgMappingComplete(exportDir)
	if err != nil {
		return err
	}
	if complete {
		return nil
	}
	csvPath := filepath.Join(exportDir, "organizations.csv")
	return fmt.Errorf(
		"organizations.csv has %d row(s) without sonarcloud_org_key. "+
			"Edit %s to set the target SonarQube Cloud organization for each row, "+
			"then re-run migrate to continue.",
		missing, csvPath,
	)
}

// printMappingPrompt is the success-side companion to requireOrgMapping —
// emitted by migrate's first pass so the user knows what to do next. Kept
// as a separate function so callers can show or suppress the prompt as
// needed (e.g. in tests).
func printMappingPrompt(exportDir string) {
	csvPath := filepath.Join(exportDir, "organizations.csv")
	fmt.Fprintf(os.Stderr,
		"\nNext: edit %s to set sonarcloud_org_key for each row, "+
			"then re-run migrate to continue from this point.\n",
		csvPath,
	)
}
