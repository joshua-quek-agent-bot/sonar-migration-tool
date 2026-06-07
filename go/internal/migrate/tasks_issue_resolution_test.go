package migrate

import "testing"

// TestGetFallbackTransition covers the SQS-resolution / status → Cloud-transition
// mapping. See issue #322: ACCEPTED must map to the "accept" transition
// (MQR / Clean as You Code) and not be collapsed into "wontfix".
func TestGetFallbackTransition(t *testing.T) {
	tests := []struct {
		name       string
		resolution string
		status     string
		want       string
	}{
		// Resolution-based mappings (highest priority).
		{
			name:       "FALSE-POSITIVE resolution maps to falsepositive",
			resolution: "FALSE-POSITIVE",
			status:     "",
			want:       "falsepositive",
		},
		{
			name:       "WONTFIX resolution maps to wontfix",
			resolution: "WONTFIX",
			status:     "",
			want:       "wontfix",
		},
		// Status-based mappings.
		{
			name:       "CONFIRMED status maps to confirm",
			resolution: "",
			status:     "CONFIRMED",
			want:       "confirm",
		},
		{
			name:       "REOPENED status maps to reopen",
			resolution: "",
			status:     "REOPENED",
			want:       "reopen",
		},
		{
			name:       "OPEN status has no transition",
			resolution: "",
			status:     "OPEN",
			want:       "",
		},
		{
			name:       "RESOLVED status maps to resolve",
			resolution: "",
			status:     "RESOLVED",
			want:       "resolve",
		},
		{
			name:       "CLOSED status maps to resolve",
			resolution: "",
			status:     "CLOSED",
			want:       "resolve",
		},
		// Issue #322 regression: ACCEPTED must map to "accept", not "wontfix".
		{
			name:       "ACCEPTED status maps to accept (issue #322)",
			resolution: "",
			status:     "ACCEPTED",
			want:       "accept",
		},
		{
			name:       "lowercase accepted status maps to accept",
			resolution: "",
			status:     "accepted",
			want:       "accept",
		},
		{
			name:       "ACCEPTED resolution falls through to status (returns empty)",
			// ACCEPTED is a SonarQube status, not a resolution; the resolution
			// branch only recognises FALSE-POSITIVE and WONTFIX. The caller
			// should pass ACCEPTED via the status field.
			resolution: "ACCEPTED",
			status:     "",
			want:       "",
		},
		{
			name:       "FALSE_POSITIVE status maps to falsepositive",
			resolution: "",
			status:     "FALSE_POSITIVE",
			want:       "falsepositive",
		},
		{
			name:       "IN_SANDBOX status has no transition",
			resolution: "",
			status:     "IN_SANDBOX",
			want:       "",
		},
		{
			name:       "unknown resolution and empty status returns empty",
			resolution: "",
			status:     "",
			want:       "",
		},
		{
			name:       "unknown status returns empty",
			resolution: "",
			status: "BOGUS_STATUS",
			want:       "",
		},
		// Resolution priority: FALSE-POSITIVE wins over OPEN status.
		{
			name:       "resolution takes priority over status",
			resolution: "FALSE-POSITIVE",
			status:     "OPEN",
			want:       "falsepositive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getFallbackTransition(tc.resolution, tc.status)
			if got != tc.want {
				t.Errorf("getFallbackTransition(%q, %q) = %q, want %q",
					tc.resolution, tc.status, got, tc.want)
			}
		})
	}
}
