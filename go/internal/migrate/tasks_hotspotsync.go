package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sonar-solutions/sonar-migration-tool/internal/common"
	"github.com/sonar-solutions/sonar-migration-tool/internal/structure"
	"github.com/sonar-solutions/sq-api-go/types"
	"golang.org/x/sync/errgroup"
)

// hotspotMetadataSyncTasks returns the task definitions for syncing hotspot
// metadata (status, resolution, comments) from SonarQube Server to Cloud.
func hotspotMetadataSyncTasks() []TaskDef {
	return []TaskDef{{
		Name:         "syncHotspotMetadata",
		Editions:     common.AllEditions,
		Dependencies: []string{"importScanHistory"},
		Run:          runSyncHotspotMetadata,
	}}
}

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// matchableHotspot is a normalised hotspot representation used for FIFO
// matching between source (SonarQube Server) and target (SonarQube Cloud).
type matchableHotspot struct {
	Key        string
	RuleKey    string
	Component  string
	Line       int
	Status     string
	Resolution string
	Comments   []hotspotComment
}

// hotspotComment captures a single comment attached to a hotspot.
type hotspotComment struct {
	Login     string
	HTMLText  string
	Markdown  string
	CreatedAt string
}

// hotspotPair links a source hotspot to its matched Cloud counterpart.
type hotspotPair struct {
	source matchableHotspot
	cloud  matchableHotspot
}

// ---------------------------------------------------------------------------
// Matching helpers
// ---------------------------------------------------------------------------

// buildHotspotMatchKey produces "ruleKey|filePath|line" for FIFO matching.
// The component prefix (everything up to and including the first colon after
// the project key) is stripped so that source and Cloud components can be
// compared by relative file path alone.
func buildHotspotMatchKey(h matchableHotspot, projectKey string) string {
	if h.RuleKey == "" || h.Component == "" || h.Line <= 0 {
		return ""
	}
	filePath := h.Component
	prefix := projectKey + ":"
	if strings.HasPrefix(filePath, prefix) {
		filePath = filePath[len(prefix):]
	}
	return fmt.Sprintf("%s|%s|%d", h.RuleKey, filePath, h.Line)
}

// matchHotspots performs FIFO matching between source and cloud hotspot
// slices. For each unique match key the first unmatched source hotspot is
// paired with the first unmatched cloud hotspot that shares the same key.
// This is identical in semantics to matchIssues.
func matchHotspots(sources, clouds []matchableHotspot, sourceProject, cloudProject string) []hotspotPair {
	// Build cloud buckets keyed by match key.
	type bucket struct {
		items []matchableHotspot
		idx   int // next unconsumed index
	}
	cloudBuckets := make(map[string]*bucket)
	for _, c := range clouds {
		k := buildHotspotMatchKey(c, cloudProject)
		if k == "" {
			continue
		}
		b, ok := cloudBuckets[k]
		if !ok {
			b = &bucket{}
			cloudBuckets[k] = b
		}
		b.items = append(b.items, c)
	}

	var pairs []hotspotPair
	for _, s := range sources {
		k := buildHotspotMatchKey(s, sourceProject)
		if k == "" {
			continue
		}
		b, ok := cloudBuckets[k]
		if !ok || b.idx >= len(b.items) {
			continue
		}
		pairs = append(pairs, hotspotPair{source: s, cloud: b.items[b.idx]})
		b.idx++
	}
	return pairs
}

// ---------------------------------------------------------------------------
// Resolution mapping
// ---------------------------------------------------------------------------

// hotspotResolutionResult is the outcome of mapping a SonarQube Server
// hotspot resolution onto a SonarQube Cloud resolution.
//
// Mapped is the value we will post to Cloud via ChangeStatus. The
// Acknowledged / Unknown flags let the caller log a warning and surface
// the count in the migration report instead of silently downgrading the
// resolution — see issue #323 (CLOUDVOYAGER-DELTA BUG-07).
type hotspotResolutionResult struct {
	// Mapped is the Cloud resolution value to post. "" means
	// ChangeStatus should be called without a resolution parameter,
	// which is the safest behaviour for a value we don't recognise.
	Mapped string

	// Acknowledged is true when the source resolution was ACKNOWLEDGED,
	// which SonarCloud does not support. The closest supported
	// resolution is SAFE, which we use as the Mapped value, but the
	// flag lets the caller record the fidelity loss.
	Acknowledged bool

	// Unknown is true when the source resolution was empty, lowercased
	// garbage, or a value not in the recognised set. The previous
	// implementation silently returned SAFE for every unknown value,
	// which made it impossible to tell ACKNOWLEDGED-apart-from-SAFE
	// from "user typo" or "future SQS resolution". With Unknown set,
	// the caller can log a warning and let a human investigate.
	Unknown bool
}

// mapHotspotResolution converts a SonarQube Server hotspot resolution
// into the equivalent SonarQube Cloud resolution value.
//
// ACKNOWLEDGED has no direct equivalent in SonarCloud; we map it to
// SAFE and set Acknowledged=true so the caller can surface the
// downgrade in the migration report. Reference:
// https://docs.sonarsource.com/sonarqube-cloud/using-sonarqube-cloud/reviewing-security-hotspots/
// (SonarCloud hotspot resolutions are SAFE, FIXED, or "no resolution"
// for TO_REVIEW — see the hotspot change_status endpoint.)
//
// Truly unknown values return Mapped="" so we don't fabricate a SAFE
// value and silently lose the source signal.
func mapHotspotResolution(resolution string) hotspotResolutionResult {
	switch strings.ToUpper(resolution) {
	case "SAFE":
		return hotspotResolutionResult{Mapped: "SAFE"}
	case "FIXED":
		return hotspotResolutionResult{Mapped: "FIXED"}
	case "ACKNOWLEDGED":
		// SonarCloud does not have an ACKNOWLEDGED resolution; SAFE
		// is the closest supported value. We still flag it so the
		// migration report can list it as an expected fidelity loss.
		return hotspotResolutionResult{Mapped: "SAFE", Acknowledged: true}
	default:
		return hotspotResolutionResult{Unknown: true}
	}
}

// ---------------------------------------------------------------------------
// Main task entry point
// ---------------------------------------------------------------------------

// runSyncHotspotMetadata is the Run function for the syncHotspotMetadata task.
// It iterates over every project created during migration and synchronises
// hotspot statuses and comments from the SonarQube Server extract to Cloud.
func runSyncHotspotMetadata(ctx context.Context, e *Executor) error {
	return forEachMigrateItem(ctx, e, "syncHotspotMetadata", "createProjects",
		func(ctx context.Context, item json.RawMessage, w *common.ChunkWriter) error {
			cloudKey := extractField(item, "cloud_project_key")
			orgKey := extractField(item, "sonarcloud_org_key")
			serverURL := extractField(item, "server_url")
			serverKey := extractField(item, "key")

			if cloudKey == "" || orgKey == "" {
				return nil
			}

			result, err := syncProjectHotspots(ctx, e, syncHotspotInput{
				CloudKey:  cloudKey,
				OrgKey:    orgKey,
				ServerURL: serverURL,
				ServerKey: serverKey,
			})
			if err != nil {
				logAPIWarn(e.Logger, "syncHotspotMetadata: project failed", err,
					"project", cloudKey)
			}

			record, _ := json.Marshal(map[string]any{
				"cloud_project_key":      cloudKey,
				"synced":                 result.Synced,
				"skipped":                result.Skipped,
				"failed":                 result.Failed,
				"acknowledged_downgrade": result.Acknowledged,
				"unknown_resolution":     result.UnknownResolution,
				"error":                  result.Error,
			})
			return w.WriteOne(record)
		})
}

// ---------------------------------------------------------------------------
// Per-project sync
// ---------------------------------------------------------------------------

type syncHotspotInput struct {
	CloudKey  string
	OrgKey    string
	ServerURL string
	ServerKey string
}

type syncHotspotResult struct {
	Synced            int64
	Skipped           int64
	Failed            int64
	Acknowledged      int64 // count of hotspots whose source resolution was ACKNOWLEDGED (downgraded to SAFE)
	UnknownResolution int64 // count of hotspots whose source resolution was empty or unrecognised
	Error             string
}

// syncProjectHotspots synchronises hotspot metadata for a single project.
func syncProjectHotspots(ctx context.Context, e *Executor, input syncHotspotInput) (syncHotspotResult, error) {
	counter := NewTaskCounter("syncHotspotMetadata:" + input.CloudKey)
	defer counter.LogSummary(e.Logger)

	matchedPairs, allCount, err := buildHotspotPairs(ctx, e, input)
	if err != nil {
		return syncHotspotResult{Error: err.Error()}, err
	}
	if len(matchedPairs) == 0 {
		return syncHotspotResult{Skipped: int64(allCount)}, nil
	}

	// Sync pairs concurrently with bounded parallelism.
	// matchedPairs is fully built BEFORE launching goroutines. Each goroutine
	// operates on exactly ONE pair -- no cross-pair sharing, no race conditions.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(cap(e.Sem))

	// Atomic counters for fidelity-loss tracking (issue #323). Shared across
	// goroutines; loaded into syncHotspotResult after the loop.
	var acknowledged, unknownRes atomic.Int64

	for i := range matchedPairs {
		pair := matchedPairs[i]
		g.Go(func() error {
			if gctx.Err() != nil {
				return nil
			}
			if err := syncOneHotspot(gctx, e, pair, &acknowledged, &unknownRes, input.ServerURL, input.ServerKey); err != nil {
				counter.Fail()
				logAPIWarn(e.Logger, "syncHotspotMetadata: hotspot sync failed", err,
					"source_key", pair.source.Key, "cloud_key", pair.cloud.Key)
			} else {
				counter.Success()
			}
			return nil
		})
	}
	g.Wait() //nolint:errcheck // goroutines always return nil; errors are per-pair

	return syncHotspotResult{
		Synced:            counter.succeeded.Load(),
		Skipped:           int64(allCount - len(matchedPairs)),
		Failed:            counter.failed.Load(),
		Acknowledged:      acknowledged.Load(),
		UnknownResolution: unknownRes.Load(),
	}, nil
}

// filterActionableHotspotPairs returns pairs that need any work: REVIEWED status
// sync, or any TO_REVIEW/REVIEWED pair that has source comments to migrate.
func filterActionableHotspotPairs(pairs []hotspotPair) []hotspotPair {
	var actionable []hotspotPair
	for _, p := range pairs {
		needsStatusSync := strings.ToUpper(p.source.Status) == "REVIEWED"
		needsCommentSync := len(p.source.Comments) > 0
		if needsStatusSync || needsCommentSync {
			actionable = append(actionable, p)
		}
	}
	return actionable
}

// buildHotspotPairs loads, indexes, matches, and filters hotspot pairs for a
// project. Returns only actionable pairs that need syncing.
func buildHotspotPairs(ctx context.Context, e *Executor, input syncHotspotInput) ([]hotspotPair, int, error) {
	sourceHotspots, err := loadMatchableHotspots(e, input.ServerURL, input.ServerKey)
	if err != nil {
		return nil, 0, err
	}
	if len(sourceHotspots) == 0 {
		return nil, 0, nil
	}

	_ = waitForCloudIndexing(ctx, func() (int, error) {
		return e.Cloud.Hotspots.Count(ctx, input.CloudKey, input.OrgKey)
	})

	cloudAPIHotspots, err := e.Cloud.Hotspots.SearchAll(ctx, input.CloudKey, input.OrgKey)
	if err != nil {
		return nil, 0, err
	}
	cloudHotspots := loadCloudMatchableHotspots(cloudAPIHotspots)

	allPairs := matchHotspots(sourceHotspots, cloudHotspots, input.ServerKey, input.CloudKey)
	actionable := filterActionableHotspotPairs(allPairs)

	e.Logger.Info("syncHotspotMetadata: matched pairs",
		"project", input.CloudKey,
		"source_total", len(sourceHotspots),
		"cloud_total", len(cloudHotspots),
		"matched", len(allPairs),
		"actionable", len(actionable),
	)

	return actionable, len(allPairs), nil
}

// ---------------------------------------------------------------------------
// Per-hotspot sync
// ---------------------------------------------------------------------------

// syncOneHotspot synchronises a single hotspot's status and comments.
// Operations are sequential within each hotspot: status first, then comments.
//
// The acknowledged and unknownRes counters are passed in by the caller so
// that the migration report can list fidelity losses (e.g. ACKNOWLEDGED
// hotspots downgraded to SAFE) and resolution-mapping gaps (e.g. an
// unrecognised future SQS resolution). They are atomic because
// syncOneHotspot is invoked from many goroutines.
//
// serverURL and serverKey are the source SonarQube Server endpoint and
// project key, used to build a deep-link comment back to the source
// hotspot (see #321).
func syncOneHotspot(ctx context.Context, e *Executor, pair hotspotPair, acknowledged, unknownRes *atomic.Int64, serverURL, serverKey string) error {
	// 1. Sync status: if source is REVIEWED, change Cloud hotspot status.
	if strings.ToUpper(pair.source.Status) == "REVIEWED" {
		res := mapHotspotResolution(pair.source.Resolution)
		if res.Acknowledged {
			acknowledged.Add(1)
			e.Logger.Warn("syncHotspotMetadata: ACKNOWLEDGED hotspot downgraded to SAFE — SonarCloud has no ACKNOWLEDGED resolution",
				"source_key", pair.source.Key,
				"cloud_key", pair.cloud.Key,
				"source_resolution", pair.source.Resolution)
		}
		if res.Unknown {
			unknownRes.Add(1)
			e.Logger.Warn("syncHotspotMetadata: unrecognised hotspot resolution; posting without a resolution value",
				"source_key", pair.source.Key,
				"cloud_key", pair.cloud.Key,
				"source_resolution", pair.source.Resolution)
		}
		if err := e.Cloud.Hotspots.ChangeStatus(ctx, pair.cloud.Key, "REVIEWED", res.Mapped); err != nil {
			return fmt.Errorf("change status: %w", err)
		}
	}

	// 2. Sync comments: fetch Cloud detail first for idempotency check.
	if len(pair.source.Comments) == 0 {
		// No source comments to migrate. Still post the source-link
		// comment if the URL fields are populated; fetch the Cloud
		// hotspot's current comments for the idempotency check.
		_ = syncHotspotSourceLinkComment(ctx, e, pair.cloud.Key, fetchCloudHotspotComments(ctx, e, pair.cloud.Key), serverURL, serverKey, pair.source.Key)
		return nil
	}

	cloudComments := fetchCloudHotspotComments(ctx, e, pair.cloud.Key)
	for _, comment := range pair.source.Comments {
		if isAlreadyMigratedComment(comment, cloudComments) {
			continue
		}

		text := formatMigratedHotspotComment(comment)
		if text == "" {
			continue
		}

		if err := e.Cloud.Hotspots.AddComment(ctx, pair.cloud.Key, text); err != nil {
			logAPIWarn(e.Logger, "syncHotspotMetadata: add comment failed", err,
				"cloud_key", pair.cloud.Key,
				"comment_author", comment.Login,
			)
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// 3. Post the source-link comment (idempotent). Non-fatal — failure is
	// logged but does not fail the whole hotspot sync. See issue #321.
	_ = syncHotspotSourceLinkComment(ctx, e, pair.cloud.Key, cloudComments, serverURL, serverKey, pair.source.Key)

	return nil
}

// fetchCloudHotspotComments retrieves comments for a Cloud hotspot via the
// /api/hotspots/show endpoint. Returns nil on failure (non-fatal — worst case
// is duplicate comments, not data loss).
func fetchCloudHotspotComments(ctx context.Context, e *Executor, cloudKey string) []hotspotComment {
	detail, err := e.Cloud.Hotspots.Show(ctx, cloudKey)
	if err != nil {
		e.Logger.Debug("syncHotspotMetadata: could not fetch cloud hotspot detail",
			"cloud_key", cloudKey, "err", err)
		return nil
	}
	comments := make([]hotspotComment, 0, len(detail.Comment))
	for _, c := range detail.Comment {
		comments = append(comments, hotspotComment{
			Login:    c.Login,
			HTMLText: c.HTMLText,
			Markdown: c.Markdown,
		})
	}
	return comments
}

// migratedCommentPrefix is prepended to every migrated comment.
const migratedCommentPrefix = "[Migrated from SonarQube]"

// formatMigratedHotspotComment builds the comment text to post to Cloud.
func formatMigratedHotspotComment(c hotspotComment) string {
	body := c.Markdown
	if body == "" {
		body = c.HTMLText
	}
	if body == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(migratedCommentPrefix)
	sb.WriteString("\n\n")
	if c.Login != "" {
		sb.WriteString("**Author:** ")
		sb.WriteString(c.Login)
		sb.WriteString("\n")
	}
	if c.CreatedAt != "" {
		sb.WriteString("**Date:** ")
		sb.WriteString(c.CreatedAt)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(body)
	return sb.String()
}

// isAlreadyMigratedComment checks whether a source comment has already been
// migrated to Cloud by looking for the migration prefix in Cloud comments.
func isAlreadyMigratedComment(source hotspotComment, cloudComments []hotspotComment) bool {
	body := source.Markdown
	if body == "" {
		body = source.HTMLText
	}
	if body == "" {
		return true // empty comment, treat as already handled
	}

	for _, cc := range cloudComments {
		ccText := cc.Markdown
		if ccText == "" {
			ccText = cc.HTMLText
		}
		if strings.Contains(ccText, migratedCommentPrefix) && strings.Contains(ccText, body) {
			return true
		}
	}
	return false
}

// sourceLinkHotspotCommentPrefix is the marker prepended to the source-link
// comment posted at the end of every hotspot migration. The check against
// this prefix makes the post idempotent — re-running the task does not
// create duplicate link comments. See issue #321.
const sourceLinkHotspotCommentPrefix = "Link to [Original hotspot]("

// syncHotspotSourceLinkComment posts a final comment on the Cloud hotspot
// that deep-links back to the original SonarQube Server hotspot, so
// reviewers on Cloud can find the source of truth in one click.
// Idempotent: if a comment beginning with the source-link prefix already
// exists, no new comment is posted.
//
// Non-fatal: returns true on API failure (logged inside).
func syncHotspotSourceLinkComment(ctx context.Context, e *Executor, cloudKey string, cloudComments []hotspotComment, serverURL, serverKey, sourceHotspotKey string) bool {
	if serverURL == "" || serverKey == "" || sourceHotspotKey == "" {
		return false
	}
	if hasSourceLinkComment(sourceLinkHotspotCommentPrefix, cloudComments) {
		return false
	}
	url := buildSourceHotspotLinkURL(serverURL, serverKey, sourceHotspotKey)
	text := fmt.Sprintf("Link to [Original hotspot](%s)", url)
	if err := e.Cloud.Hotspots.AddComment(ctx, cloudKey, text); err != nil {
		logAPIWarn(e.Logger, "syncHotspotMetadata: source-link comment failed", err,
			"hotspot", cloudKey, "url", url)
		return true
	}
	e.Logger.Debug("syncHotspotMetadata: source-link comment posted",
		"hotspot", cloudKey, "url", url)
	return false
}

// buildSourceHotspotLinkURL constructs the SonarQube Server deep-link URL
// for a given hotspot. Format:
//   ${serverURL}/security_hotspots?id=${serverProjectKey}&hotspots=${hotspotKey}
func buildSourceHotspotLinkURL(serverURL, serverKey, hotspotKey string) string {
	return fmt.Sprintf("%s/security_hotspots?id=%s&hotspots=%s",
		strings.TrimRight(serverURL, "/"),
		url.QueryEscape(serverKey),
		url.QueryEscape(hotspotKey),
	)
}

// hasSourceLinkComment returns true when a comment beginning with prefix
// (the source-link marker) is already present in the Cloud hotspot
// comment list. Mirrors isAlreadyMigratedSourceLinkComment in the issue
// flow but operates on hotspot comments.
func hasSourceLinkComment(prefix string, cloudComments []hotspotComment) bool {
	for _, cc := range cloudComments {
		ccText := cc.Markdown
		if ccText == "" {
			ccText = cc.HTMLText
		}
		if strings.HasPrefix(ccText, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Loading source hotspots from extract data
// ---------------------------------------------------------------------------

// loadMatchableHotspots reads the getProjectHotspotsFull extract items and
// converts them into normalised matchableHotspot structs.
func loadMatchableHotspots(e *Executor, serverURL, serverKey string) ([]matchableHotspot, error) {
	items, err := readExtractItems(e, "getProjectHotspotsFull")
	if err != nil {
		return nil, fmt.Errorf("loadMatchableHotspots: %w", err)
	}

	var hotspots []matchableHotspot
	for _, item := range items {
		if item.ServerURL != serverURL {
			continue
		}

		// Filter by project key.
		projKey := extractField(item.Data, "project")
		if projKey == "" {
			projKey = extractField(item.Data, "projectKey")
		}
		if projKey != serverKey {
			continue
		}

		h := parseMatchableHotspot(item.Data)
		if h.Key == "" {
			continue
		}
		hotspots = append(hotspots, h)
	}
	return hotspots, nil
}

// parseMatchableHotspot extracts a matchableHotspot from a raw JSON object.
func parseMatchableHotspot(data json.RawMessage) matchableHotspot {
	key := extractField(data, "key")
	component := extractField(data, "component")
	status := extractField(data, "status")
	resolution := extractField(data, "resolution")
	line := extractHotspotLine(data)

	// ruleKey may be at top level or nested inside a "rule" object.
	ruleKey := extractField(data, "ruleKey")
	if ruleKey == "" {
		ruleKey = extractNestedField(data, "rule", "key")
	}

	comments := parseHotspotComments(data)

	return matchableHotspot{
		Key:        key,
		RuleKey:    ruleKey,
		Component:  component,
		Line:       line,
		Status:     status,
		Resolution: resolution,
		Comments:   comments,
	}
}

// extractHotspotLine reads the "line" field from a hotspot JSON object.
func extractHotspotLine(data json.RawMessage) int {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return 0
	}
	raw, ok := obj["line"]
	if !ok {
		return 0
	}
	var v int
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	// Try as string.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ := strconv.Atoi(s)
		return n
	}
	return 0
}

// extractNestedField reads obj[outerKey][innerKey] as a string.
func extractNestedField(data json.RawMessage, outerKey, innerKey string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return ""
	}
	nested, ok := obj[outerKey]
	if !ok {
		return ""
	}
	return extractField(nested, innerKey)
}

// ---------------------------------------------------------------------------
// Loading Cloud hotspots
// ---------------------------------------------------------------------------

// loadCloudMatchableHotspots converts Cloud API Hotspot structs into
// matchableHotspot structs suitable for FIFO matching.
func loadCloudMatchableHotspots(hotspots []types.Hotspot) []matchableHotspot {
	result := make([]matchableHotspot, 0, len(hotspots))
	for _, h := range hotspots {
		result = append(result, matchableHotspot{
			Key:        h.Key,
			RuleKey:    h.RuleKey,
			Component:  h.Component,
			Line:       h.Line,
			Status:     h.Status,
			Resolution: h.Resolution,
		})
	}
	return result
}

// ---------------------------------------------------------------------------
// Parsing comments from extract data
// ---------------------------------------------------------------------------

// parseHotspotComments extracts the comment array from a hotspot detail JSON.
// The SonarQube API uses "comment" (singular) as the field name for the array.
func parseHotspotComments(data json.RawMessage) []hotspotComment {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}

	// The field name is "comment" (singular) in the hotspot detail response.
	raw, ok := obj["comment"]
	if !ok {
		// Also try "comments" (plural) in case extract data uses a different format.
		raw, ok = obj["comments"]
		if !ok {
			return nil
		}
	}

	var rawComments []json.RawMessage
	if err := json.Unmarshal(raw, &rawComments); err != nil {
		return nil
	}

	comments := make([]hotspotComment, 0, len(rawComments))
	for _, rc := range rawComments {
		c := hotspotComment{
			Login:     extractField(rc, "login"),
			HTMLText:  extractField(rc, "htmlText"),
			Markdown:  extractField(rc, "markdown"),
			CreatedAt: extractField(rc, "createdAt"),
		}
		if c.HTMLText != "" || c.Markdown != "" {
			comments = append(comments, c)
		}
	}
	return comments
}

// ---------------------------------------------------------------------------
// Compile-time interface assertion to ensure ExtractItem is used correctly.
// ---------------------------------------------------------------------------

var _ = (structure.ExtractItem{}).ServerURL
