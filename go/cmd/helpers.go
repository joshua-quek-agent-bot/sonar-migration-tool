package cmd

import (
	"fmt"
	"os"
)

// printPhase writes the "[N/M] <description>" banner used at the start of
// each chained phase in transfer / migrate so the literal strings live in
// one place.
func printPhase(step, total int, description string) {
	fmt.Printf("[%d/%d] %s\n", step, total, description)
}

// warnSkippedProjects prints a warning listing project keys that the
// extract phase reported as skipped (typically due to insufficient
// permissions) so users notice and can re-run with an elevated token.
func warnSkippedProjects(skipped []string) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: %d project(s) skipped (insufficient privileges):\n", len(skipped))
	for _, k := range skipped {
		fmt.Fprintf(os.Stderr, "  - %s\n", k)
	}
}
