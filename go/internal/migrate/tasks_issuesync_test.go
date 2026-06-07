package migrate

import (
	"testing"
)

func TestBuildSourceIssueLinkURL(t *testing.T) {
	tests := []struct {
		name        string
		serverURL   string
		serverKey   string
		issueKey    string
		wantContain string
	}{
		{
			name:        "basic URL",
			serverURL:   "https://sonar.example.com",
			serverKey:   "my-project",
			issueKey:    "AX-123",
			wantContain: "/project/issues?id=my-project&issues=AX-123&open=AX-123",
		},
		{
			name:        "trailing slash on serverURL is stripped",
			serverURL:   "https://sonar.example.com/",
			serverKey:   "my-project",
			issueKey:    "AX-123",
			wantContain: "https://sonar.example.com/project/issues",
		},
		{
			name:        "special chars in keys are URL-escaped",
			serverURL:   "https://sonar.example.com",
			serverKey:   "my project/with spaces",
			issueKey:    "AX 1",
			wantContain: "id=my+project%2Fwith+spaces&issues=AX+1&open=AX+1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSourceIssueLinkURL(tc.serverURL, tc.serverKey, tc.issueKey)
			if !contains(got, tc.wantContain) {
				t.Errorf("buildSourceIssueLinkURL(%q, %q, %q) = %q, want substring %q",
					tc.serverURL, tc.serverKey, tc.issueKey, got, tc.wantContain)
			}
			// ServerURL must always be at the start.
			prefix := tc.serverURL
			if len(got) < len(prefix) || got[:len(prefix)] != prefix {
				t.Errorf("URL does not start with serverURL: got %q", got)
			}
		})
	}
}

func TestIsAlreadyMigratedSourceLinkComment(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		cloudComments []issueComment
		want          bool
	}{
		{
			name:          "no cloud comments",
			prefix:        "Link to [Original issue](",
			cloudComments: nil,
			want:          false,
		},
		{
			name:   "matching prefix present",
			prefix: "Link to [Original issue](",
			cloudComments: []issueComment{
				{Markdown: "Link to [Original issue](https://sonar.example.com/project/issues?id=p&issues=AX-1&open=AX-1)"},
			},
			want: true,
		},
		{
			name:   "different prefix does not match",
			prefix: "Link to [Original issue](",
			cloudComments: []issueComment{
				{Markdown: "Link to [Original hotspot](https://sonar.example.com/security_hotspots?id=p&hotspots=AX-1)"},
			},
			want: false,
		},
		{
			name:   "matches against HTMLText when Markdown empty",
			prefix: "Link to [Original issue](",
			cloudComments: []issueComment{
				{HTMLText: "Link to [Original issue](https://x)"},
			},
			want: true,
		},
		{
			name:   "unrelated comment does not match",
			prefix: "Link to [Original issue](",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from alice]\n\nhello"},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAlreadyMigratedSourceLinkComment(tc.prefix, tc.cloudComments)
			if got != tc.want {
				t.Errorf("isAlreadyMigratedSourceLinkComment(%q, ...) = %v, want %v",
					tc.prefix, got, tc.want)
			}
		})
	}
}

// TestSourceLinkIssueCommentIdempotency models the "called twice" flow
// required by issue #321: the first invocation would post a comment
// containing the source-link prefix; the second invocation observes that
// prefix in the cloud-comments list and short-circuits without re-posting.
//
// This test does not need a real Cloud client — it exercises the
// idempotency check (the prefix is detected) AND the URL builder (the
// comment text that would be posted). Together these prove that the
// "posted exactly once" invariant holds at the unit level.
func TestSourceLinkIssueCommentIdempotency(t *testing.T) {
	const (
		serverURL = "https://sonar.example.com"
		serverKey = "my-project"
		sourceKey = "AX-100"
		prefix    = "Link to [Original issue]("
	)
	url := buildSourceIssueLinkURL(serverURL, serverKey, sourceKey)
	text := "Link to [Original issue](" + url + ")"

	// Pre-call: no comments present → first call would post.
	var cloudComments []issueComment
	if isAlreadyMigratedSourceLinkComment(prefix, cloudComments) {
		t.Fatalf("first call: expected NOT to detect existing source-link comment")
	}

	// Simulate Cloud now containing the freshly-posted comment.
	cloudComments = []issueComment{{Markdown: text}}

	// Re-call: idempotency check must return true so the post is skipped.
	if !isAlreadyMigratedSourceLinkComment(prefix, cloudComments) {
		t.Errorf("second call: expected idempotency check to find existing source-link comment")
	}

	// URL format regression guard — issue #321 requires this exact shape.
	want := "https://sonar.example.com/project/issues?id=my-project&issues=AX-100&open=AX-100"
	if url != want {
		t.Errorf("source-link URL = %q, want %q", url, want)
	}
}

// contains is a tiny helper to avoid importing strings just for one call.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestIsAlreadyMigratedIssueComment(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		cloudComments []issueComment
		want         bool
	}{
		{
			name: "no cloud comments",
			body: "some comment text",
			cloudComments: nil,
			want: false,
		},
		{
			name: "cloud comment contains prefix and body",
			body: "some comment text",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from admin on 2024-01-01]\n\nsome comment text"},
			},
			want: true,
		},
		{
			name: "cloud comment has prefix but different body",
			body: "some comment text",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from admin on 2024-01-01]\n\ndifferent comment"},
			},
			want: false,
		},
		{
			name: "cloud comment contains body but no prefix",
			body: "some comment text",
			cloudComments: []issueComment{
				{Markdown: "some comment text"},
			},
			want: false,
		},
		{
			name: "matches on HTMLText when Markdown is empty",
			body: "html comment",
			cloudComments: []issueComment{
				{HTMLText: "[Migrated from bob]\n\nhtml comment"},
			},
			want: true,
		},
		{
			name: "prefers Markdown over HTMLText",
			body: "real body",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from bob]\n\nreal body", HTMLText: "ignored"},
			},
			want: true,
		},
		{
			name: "no match among multiple cloud comments",
			body: "target body",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from alice]\n\nother body"},
				{Markdown: "plain comment without prefix"},
			},
			want: false,
		},
		{
			name: "match found in second of multiple cloud comments",
			body: "target body",
			cloudComments: []issueComment{
				{Markdown: "[Migrated from alice]\n\nother body"},
				{Markdown: "[Migrated from bob]\n\ntarget body"},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAlreadyMigratedIssueComment(tc.body, tc.cloudComments)
			if got != tc.want {
				t.Errorf("isAlreadyMigratedIssueComment(%q, ...) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
