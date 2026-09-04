package diffview

import (
	"os"
	"strings"
	"testing"
)

func TestParseHunkOnlySnippetUsesNeutralFilename(t *testing.T) {
	presentation := Parse("@@ -1 +1 @@ replace handler\n-old\n+new")
	if presentation.ParseErr != nil {
		t.Fatal(presentation.ParseErr)
	}
	if len(presentation.Hunks) != 1 || presentation.Hunks[0].FileName != "file" {
		t.Fatalf("hunks = %#v, want one neutral-file hunk", presentation.Hunks)
	}
	if got := RenderHunkHeader(presentation.Hunks[0]); !strings.Contains(got, "Hunk 1") || !strings.Contains(got, "file") || !strings.Contains(got, "replace handler") {
		t.Fatalf("hunk heading = %q", got)
	}
}

func TestReviewUIDemoFixtureParses(t *testing.T) {
	content, err := os.ReadFile("../../../.agents/jig/fixtures/review-ui-demo.diff")
	if err != nil {
		t.Fatal(err)
	}
	presentation := Parse(string(content))
	if presentation.ParseErr != nil {
		t.Fatal(presentation.ParseErr)
	}
	if len(presentation.Hunks) < 5 {
		t.Fatalf("hunks = %d, want a production-sized fixture", len(presentation.Hunks))
	}
	rows := presentation.DisplayRows(nil)
	if len(rows) == 0 || !rows[0].Separator && rows[0].PatchLine == 0 {
		t.Fatalf("display rows = %#v", rows)
	}
}
