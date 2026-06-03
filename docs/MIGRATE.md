# Using `migrate` — Migrate All Projects

> **Quick decision guide**
> - Migrating **one** project and don't need to review intermediate files? Use **[`transfer`](TRANSFER.md)** — a single command that does everything.
> - Want the same multi-phase workflow but with prompts instead of typing each command? Use the **`wizard`** command — interactive and guided.
> - Want full control over each phase, multiple SonarQube Server instances, or to inspect / edit the mapping CSVs before pushing? You're in the right place — keep reading.

`migrate` is a **single-command path** that chains the full pipeline (extract → structure → mappings → migrate) and finishes by writing a PDF summary. It's the right choice when you need:

- To migrate **many** projects from a SonarQube Server instance.
- To review and edit `organizations.csv` and the per-entity mapping CSVs (`gates.csv`, `profiles.csv`, `groups.csv`, `templates.csv`, `portfolios.csv`) before pushing to SonarQube Cloud.
- To resume a failed migration from the last completed task without redoing successful work.
- To audit intermediate files for compliance or change management.
- To target multiple SonarQube Cloud organizations in a single run (multi-org mode).

The underlying `extract`, `structure`, and `mappings` commands are still available if you need to script the phases independently in CI/CD pipelines.

---

## Migration Workflow

```
┌───────────┐   ┌───────────┐   ┌───────────┐   ┌──────────┐   ┌──────────┐   ┌─────────┐
│  EXTRACT  │──►│ STRUCTURE │──►│ ORG MAP   │──►│ MAPPINGS │──►│ VALIDATE │──►│ MIGRATE │
│  Phase 1  │   │  Phase 2  │   │  Phase 3  │   │ Phase 4  │   │  Phase 5  │   │ Phase 6 │
└───────────┘   └───────────┘   └───────────┘   └──────────┘   └──────────┘   └─────────┘
```

> **Note on the diagram:** Phase 3 ("Org Map") is the human step of editing `organizations.csv` — the tool produces the file in Phase 2, you fill in the SonarQube Cloud org keys, and Phase 4 reads the result.

---

## Token permissions

| Token | Required permissions |
|---|---|
| **SonarQube Server** | Administer System, Quality Gates (read/write), Quality Profiles (read/write), Browse on all projects you want to migrate |
| **SonarQube Cloud** | Enterprise-level access, Admin on all target organizations |

For detailed token setup, see [SECURITY.md](SECURITY.md).

---

## Configuration

The `extract`, `migrate`, `reset`, and `predictive-report` commands all read the same JSON config file. The recommended shape carries one top-level block of defaults and one `source` / `target` sub-block per side of the migration. `extract` reads `source`; `migrate` / `reset` read `target`; each command silently ignores the block that isn't its own.

```jsonc
{
  "concurrency": 10,
  "timeout": 60,
  "export_directory": "./migration-files",

  "source": {
    "url":   "http://sonarqube.example.com",
    "token": "squ_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "extract_type": "all",
    "pem_file_path": null,
    "key_file_path": null,
    "cert_password": null,
    "target_task": null,
    "extract_id":  null
  },

  "target": {
    "url":   "https://sonarcloud.io/",
    "token": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "enterprise_key": "your-enterprise",
    "edition": "enterprise",
    "run_id": null,
    "target_task": null
  }
}
```

A full example lives at [`examples/config.unified.example.json`](../examples/config.unified.example.json) and a JSON Schema for editor autocomplete at [`schemas/config.schema.json`](../schemas/config.schema.json). Add the schema to your editor by referencing it in `.vscode/settings.json` or by adding a `"$schema"` pointer at the top of your config.

The three legacy shapes (flat top-level keys, `extract` / `migrate` sub-objects, and `sonarqube` + `sonarcloud` side-sectioned) still parse — existing configs keep working.

**Precedence:** CLI flags override values from the config file when both are provided. `--export_directory` on the CLI wins over `export_directory` in the config when explicitly set.

For deeper config reference, see [CONFIG.md](CONFIG.md).

---

## Step-by-step guide

All examples show both forms. Use whichever matches your setup:

- **From source:** `cd go && go run . migrate [args]`
- **Built binary:** `sonar-migration-tool migrate [args]`

> The default `--export_directory` is `./migration-files` (created in the current working directory). You can override it with the `--export_directory` flag. Every command prints `See sonar-migration-tool output results in <directory>` when it finishes.

### Step 1 — Write a unified config file

Create a `config.json` with the unified shape (one `sonarqube` block, one `sonarcloud` block). See [CONFIG.md](CONFIG.md) for the full reference.

Single-org migration:

```json
{
  "sonarqube":  { "url": "https://sonarqube.example.com", "token": "sqp_xxx" },
  "sonarcloud": { "token": "squ_xxx", "organization": "my-org" }
}
```

Multi-org migration (one SQS Server → many SonarQube Cloud orgs):

```json
{
  "sonarqube":  { "url": "https://sonarqube.example.com", "token": "sqp_xxx" },
  "sonarcloud": {
    "organizations": [
      { "key": "org-a", "token": "squ_aaa" },
      { "key": "org-b", "token": "squ_bbb" }
    ]
  }
}
```

### Step 2 — Run `migrate` (first pass)

```bash
# From source
go run . migrate --config config.json --export_directory ./files/

# Built binary
sonar-migration-tool migrate --config config.json --export_directory ./files/
```

The command runs the first two phases automatically:

```
[1/4] Extracting from SonarQube Server...
[2/4] Building organization structure...
```

After phase 2, the tool writes `files/organizations.csv` and exits with a clear message:

```
Error: organizations.csv has N row(s) without sonarcloud_org_key.
Edit ./files/organizations.csv to set the target SonarQube Cloud organization
for each row, then re-run migrate to continue.
```

This is the "human step" — see [SPEC-018 in the roadmap](../roadmap/specs/SPEC-018-multi-org-mapping.md) for why it exists and how CloudVoyager handles it the same way.

### Step 3 — Edit `organizations.csv`

Open `files/organizations.csv` in any spreadsheet or text editor. Fill in the `sonarcloud_org_key` column for every row. Save the file.

Example:

```csv
server_url,sonarcloud_org_key
http://localhost:9000,my-cloud-org-key
```

### Step 4 — Re-run `migrate` (second pass)

```bash
# From source
go run . migrate --config config.json --export_directory ./files/

# Built binary
sonar-migration-tool migrate --config config.json --export_directory ./files/
```

The tool detects that `organizations.csv` is fully populated and picks up from the Mappings phase:

```
[3/4] Generating entity mappings...
[4/4] Migrating to SonarQube Cloud...
PDF summary report: ./files/<run-id>/migration_summary.pdf
```

### CLI-only (no config file)

If you'd rather skip the JSON file, every field can be supplied on the command line:

```bash
sonar-migration-tool migrate \
  --sq-url https://sonarqube.example.com \
  --sq-token sqp_xxx \
  --sc-token squ_xxx \
  --sc-org my-org
```

### Flags

| Flag | Description |
|---|---|
| `--config` | Path to a unified JSON configuration file |
| `--sq-url` | SonarQube Server URL |
| `--sq-token` | SonarQube Server token |
| `--sq-project-key` | Scope extraction to a single SonarQube Server project key |
| `--sc-url` | SonarQube Cloud URL (default: `https://sonarcloud.io/`) |
| `--sc-token` | SonarQube Cloud token (single-org mode) |
| `--sc-org` | SonarQube Cloud organization key (single-org mode) |
| `--sc-enterprise-key` | SonarQube Cloud enterprise key (defaults to `--sc-org`) |
| `--edition` | SonarQube Cloud license edition |
| `--url` | Alias for `--sc-url` (legacy) |
| `--run_id` | Resume an in-progress migration by run ID |
| `--target_task` | Run a specific migration task (with its dependencies) |
| `--export_directory` | Working directory for intermediate files (default: `./migration-files`) |
| `--default_organization` | SonarQube Cloud organization applied to every project when `organizations.csv` has no mapping |
| `--concurrency` | Max concurrent requests |
| `--skip_profiles` | Skip quality profile migration/provisioning |
| `--include_scan_history` | Extract and import full issue/hotspot scan history |
| `--debug` | Enable debug-level logging (verbose request payloads) |

---

## Multi-server migration

If you are migrating from multiple SonarQube Server instances:

1. **Extract from each server separately** — run `extract` once per server, each time pointing to a different URL and token.
2. **Run `structure`** — this step automatically aggregates data from all extractions into a single `organizations.csv`.
3. **Edit `organizations.csv`** — fill in the `sonarcloud_org_key` for each server row.
4. **Continue with `mappings` and `migrate`** as described above. The tool handles all servers in one pass.

---

## Resuming failed operations

If a step fails partway through, you can pick up where you left off:

```bash
# Resume an extraction
sonar-migration-tool extract <URL> <TOKEN> --extract_id <PREVIOUS_EXTRACT_ID> --export_directory ./files/

# Resume a migration
sonar-migration-tool migrate <TOKEN> <ENTERPRISE_KEY> --run_id <PREVIOUS_RUN_ID> --export_directory ./files/
```

The tool tracks which tasks have already completed and skips them automatically.

---

## Additional commands

### `report` — generate a migration readiness or maturity report

```bash
# From source
go run . report --report_type migration --export_directory ./files/

# Built binary
sonar-migration-tool report --report_type migration --export_directory ./files/
```

### `predictive-report` — preview the migration before you commit

Generates the same PDF migration summary the `migrate` step produces, but *before* migrating — from the output of `extract` + `structure` and the user-edited mapping CSVs. Useful to preview how the migration will go without touching SonarQube Cloud.

```bash
# From source
go run . predictive-report --export_directory ./files/

# Built binary
sonar-migration-tool predictive-report --export_directory ./files/

# Or read export_directory from the same JSON config used by extract / migrate
sonar-migration-tool predictive-report --config extract-config.json
```

Output: `<export_directory>/predictive_migration_summary.pdf`. The `--config` flag accepts the same configuration file shape as `extract` or `migrate` — only the `export_directory` field is read. An explicit `--export_directory` flag overrides whatever the config file carries.

The Global Settings section is included with the SQS-only settings predicted to be Skipped (Setting Key column, sorted alphabetically). SonarQube Cloud API errors or rate-limiting cannot be predicted ahead of time, so they have no row in the Failed bucket.

### `analysis_report` — summarize a migration run

Parse `requests.log` into a CSV summary of API call outcomes:

```bash
# From source
go run . analysis_report <RUN_ID> --export_directory ./files/

# Built binary
sonar-migration-tool analysis_report <RUN_ID> --export_directory ./files/
```

### `reset` — undo a bad migration

Deletes all content in every org in the enterprise:

```bash
# From source
go run . reset <TOKEN> <ENTERPRISE_KEY> --export_directory ./files/

# Built binary
sonar-migration-tool reset <TOKEN> <ENTERPRISE_KEY> --export_directory ./files/
```

> **Warning:** `reset` is destructive. It deletes all migrated projects, quality profiles, quality gates, and organization configurations.

---

## After you migrate

1. Verify projects appear in SonarQube Cloud and are linked to repositories.
2. Verify quality gates and profiles are correct.
3. Re-scan all projects — unless scan history was imported, historical data does not transfer.
4. Update CI/CD pipelines to point to SonarQube Cloud (`SONAR_TOKEN` and `SONAR_HOST_URL`).
5. Generate a final report for the records:
   ```bash
   sonar-migration-tool report --report_type migration --export_directory ./files/
   ```

---

## Tips for larger instances

- Create a dedicated migration user in SonarQube Cloud with enterprise admin permissions.
- Test with a subset first using `--target_task` to migrate specific entities.
- Review the CSV mappings before running `migrate`.
- Monitor `files/<run_id>/requests.log` for API errors.
- Use `--config` with a JSON file for repeatable, scripted migrations.
- For large instances (50,000+ projects), lower concurrency and increase timeout:
  ```bash
  sonar-migration-tool extract <URL> <TOKEN> --concurrency 10 --timeout 120 --export_directory ./files/
  ```

---

## Output files reference

| File | Description |
|---|---|
| `extract.json` | Metadata about the extraction (timestamps, server info, etc.) |
| `requests.log` | Log of all API requests made during extraction |
| `results.*.jsonl` | Raw extracted data in JSON Lines format (one file per entity) |
| `organizations.csv` | Server-to-organization mapping (you edit this) |
| `projects.csv` | List of all extracted projects |
| `gates.csv` | Quality Gate mappings |
| `profiles.csv` | Quality Profile mappings |
| `groups.csv` | Group mappings |
| `templates.csv` | Permission Template mappings |
| `portfolios.csv` | Portfolio mappings |
| `predictive_migration_summary.pdf` | Output of the `predictive-report` command |
| `<export_directory>/<run_id>/requests.log` | Per-run request log for migrations |

---

## Version support

Supports SonarQube Server 6.3+. Authentication auto-detects version (Basic auth < 10, Bearer token ≥ 10). Edition-aware: Community, Developer, Enterprise, Data Center.
