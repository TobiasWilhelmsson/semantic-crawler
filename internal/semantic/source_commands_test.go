package semantic

import (
	"strings"
	"testing"
)

func TestSourceReviewCommandSelectsModelForLiveAndSnapshotSources(t *testing.T) {
	for _, tc := range []struct{ name, fixtures, mode string }{
		{"live source", "", "openai"},
		{"offline source", "tests/fixtures/generic-health-sample", "fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := sourceCard(SourceEntry{Slug: "source", Config: SourceConfig{FixtureDirectory: tc.fixtures}})
			if !strings.Contains(card.UICmd, "-model-mode "+tc.mode) {
				t.Fatalf("review retries would use the wrong model: %s", card.UICmd)
			}
		})
	}
}
