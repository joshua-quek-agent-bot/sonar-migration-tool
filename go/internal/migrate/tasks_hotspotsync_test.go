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
