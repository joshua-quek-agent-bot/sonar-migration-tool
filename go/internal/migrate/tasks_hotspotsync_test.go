package migrate

import (
	"testing"
)

func TestFilterActionableHotspotPairs(t *testing.T) {
	comment := hotspotComment{Login: "alice", Markdown: "please review"}

	tests := []struct {
		name          string
		pairs         []hotspotPair
		wantActionable int
	}{
		{
			name:          "empty input",
			pairs:         nil,
			wantActionable: 0,
		},
		{
			name: "TO_REVIEW without comments is not actionable",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "TO_REVIEW", Comments: nil}},
			},
			wantActionable: 0,
		},
		{
			name: "TO_REVIEW with comments is actionable",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "TO_REVIEW", Comments: []hotspotComment{comment}}},
			},
			wantActionable: 1,
		},
		{
			name: "REVIEWED without comments is actionable",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "REVIEWED", Comments: nil}},
			},
			wantActionable: 1,
		},
		{
			name: "REVIEWED with comments is actionable",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "REVIEWED", Comments: []hotspotComment{comment}}},
			},
			wantActionable: 1,
		},
		{
			name: "mixed bag of pairs",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "TO_REVIEW", Comments: nil}},      // not actionable
				{source: matchableHotspot{Status: "TO_REVIEW", Comments: []hotspotComment{comment}}}, // actionable (comment)
				{source: matchableHotspot{Status: "REVIEWED", Comments: nil}},        // actionable (status)
				{source: matchableHotspot{Status: "REVIEWED", Comments: []hotspotComment{comment}}},  // actionable (both)
			},
			wantActionable: 3,
		},
		{
			name: "status comparison is case-insensitive",
			pairs: []hotspotPair{
				{source: matchableHotspot{Status: "reviewed", Comments: nil}},
				{source: matchableHotspot{Status: "Reviewed", Comments: nil}},
			},
			wantActionable: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterActionableHotspotPairs(tc.pairs)
			if len(got) != tc.wantActionable {
				t.Errorf("filterActionableHotspotPairs() returned %d pairs, want %d", len(got), tc.wantActionable)
			}
		})
	}
}

func TestMapHotspotResolution(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want hotspotResolutionResult
	}{
		{
			name: "SAFE maps to SAFE",
			in:   "SAFE",
			want: hotspotResolutionResult{Mapped: "SAFE"},
		},
		{
			name: "FIXED maps to FIXED",
			in:   "FIXED",
			want: hotspotResolutionResult{Mapped: "FIXED"},
		},
		{
			name: "ACKNOWLEDGED downgrades to SAFE with flag",
			in:   "ACKNOWLEDGED",
			want: hotspotResolutionResult{Mapped: "SAFE", Acknowledged: true},
		},
		{
			name: "lowercase acknowledged still recognised",
			in:   "acknowledged",
			want: hotspotResolutionResult{Mapped: "SAFE", Acknowledged: true},
		},
		{
			name: "mixed case acknowledged still recognised",
			in:   "Acknowledged",
			want: hotspotResolutionResult{Mapped: "SAFE", Acknowledged: true},
		},
		{
			name: "empty string is unknown",
			in:   "",
			want: hotspotResolutionResult{Unknown: true},
		},
		{
			name: "garbage is unknown",
			in:   "garbage",
			want: hotspotResolutionResult{Unknown: true},
		},
		{
			name: "future SQS resolution is unknown",
			in:   "WONTFIX",
			want: hotspotResolutionResult{Unknown: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapHotspotResolution(tc.in)
			if got != tc.want {
				t.Errorf("mapHotspotResolution(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildSourceHotspotLinkURL(t *testing.T) {
	tests := []struct {
		name        string
		serverURL   string
		serverKey   string
		hotspotKey  string
		wantContain string
	}{
		{
			name:        "basic URL",
			serverURL:   "https://sonar.example.com",
			serverKey:   "my-project",
			hotspotKey:  "AX-456",
			wantContain: "/security_hotspots?id=my-project&hotspots=AX-456",
		},
		{
			name:        "trailing slash on serverURL is stripped",
			serverURL:   "https://sonar.example.com/",
			serverKey:   "my-project",
			hotspotKey:  "AX-456",
			wantContain: "https://sonar.example.com/security_hotspots",
		},
		{
			name:        "special chars in keys are URL-escaped",
			serverURL:   "https://sonar.example.com",
			serverKey:   "my project/with spaces",
			hotspotKey:  "AX 2",
			wantContain: "id=my+project%2Fwith+spaces&hotspots=AX+2",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSourceHotspotLinkURL(tc.serverURL, tc.serverKey, tc.hotspotKey)
			if !contains(got, tc.wantContain) {
				t.Errorf("buildSourceHotspotLinkURL(%q, %q, %q) = %q, want substring %q",
					tc.serverURL, tc.serverKey, tc.hotspotKey, got, tc.wantContain)
			}
		})
	}
}

func TestHasSourceLinkComment(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		cloudComments []hotspotComment
		want          bool
	}{
		{
			name:          "no cloud comments",
			prefix:        "Link to [Original hotspot](",
			cloudComments: nil,
			want:          false,
		},
		{
			name:   "matching prefix present",
			prefix: "Link to [Original hotspot](",
			cloudComments: []hotspotComment{
				{Markdown: "Link to [Original hotspot](https://sonar.example.com/security_hotspots?id=p&hotspots=AX-1)"},
			},
			want: true,
		},
		{
			name:   "issue prefix does not match hotspot prefix",
			prefix: "Link to [Original hotspot](",
			cloudComments: []hotspotComment{
				{Markdown: "Link to [Original issue](https://x)"},
			},
			want: false,
		},
		{
			name:   "unrelated comment does not match",
			prefix: "Link to [Original hotspot](",
			cloudComments: []hotspotComment{
				{Markdown: "[Migrated from SonarQube]\n\nplease review"},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hasSourceLinkComment(tc.prefix, tc.cloudComments)
			if got != tc.want {
				t.Errorf("hasSourceLinkComment(%q, ...) = %v, want %v",
					tc.prefix, got, tc.want)
			}
		})
	}
}
