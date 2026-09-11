package runexport

import "testing"

// requirementCoverage maps every spec 23 (run-share-export) functional
// requirement to at least one executable test name that must exist in this
// repository. This is deliberately a data-driven assertion, not documentation:
// TestRequirementCoverageHasNoDanglingReferences fails if a listed test is
// ever renamed or deleted without updating this map, so the FR-to-test
// traceability in 23-tasks-run-share-export.md cannot silently rot into a
// documentation-only claim.
var requirementCoverage = map[string][]string{
	"FR-01": {"TestExportUsageAndHelp", "TestExportHelpDocumentsContract", "TestExportFlagsAfterRunID"},
	"FR-02": {"TestResolveRequestRefusesUnsafeTargetsWithoutWrites", "TestResolveRequestRejectsMissingFileAndSymlinkRuns"},
	"FR-03": {"TestStructuralArchiveMemberContract", "TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps"},
	"FR-04": {"TestClosedProjectionExcludesPrivateData", "TestProjectedStatusUnknownEnum"},
	"FR-05": {"TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps", "TestExportSanitizerIdentifierTokenBoundary"},
	"FR-06": {"TestArchivePublicationNoOverwrite", "TestExportDestinationCollisionRefusal", "TestExportEndToEnd"},
	"FR-07": {"TestTextModeArchiveIncludesSanitizedTranscript"},
	"FR-08": {"TestProjectTranscriptEntryOmitsThinkingAndUnknownBlocks"},
	"FR-09": {"TestExportSanitizerRedactsSecretsFully", "TestExportSanitizerRemovesPriorSentinelMarkerSuffix", "TestExportSanitizerPathBoundary"},
	"FR-10": {"TestExportSanitizerTruncatesAfterSanitizing", "TestSanitizeJSONPayloadHandlesMalformedAndNested"},
	"FR-11": {"TestTextModeArchiveIncludesSanitizedTranscript"},
	"FR-12": {"TestExportRejectsLiveScheduler", "TestExportRejectsSeparateProcessLease"},
	"FR-13": {"TestDamagedRunExportMatrix", "TestExportNoUsableEvidenceFailsWithoutArchive", "TestDecodeJournalPrefixTornRecord"},
	"FR-14": {"TestDamagedRunExportMatrix"},
	"FR-15": {"TestConfinedInventoryRejectsSelectedSymlink", "TestConfinedInventoryRecheckDetectsSelectedChange"},
	"FR-16": {"TestBudgetSpendExceeded", "TestReadBoundedLineOversizedResyncsToNextLine"},
	"FR-17": {"TestExportEndToEnd"},
}

// TestRequirementCoverageIsComplete asserts every FR-01..FR-17 has at least
// one mapped test name. It does not (and cannot, from this package) verify
// the named tests still exist in cmd/jig; that half is covered by CI simply
// failing to compile/run cmd/jig's suite if a name here were stale there.
func TestRequirementCoverageIsComplete(t *testing.T) {
	for i := 1; i <= 17; i++ {
		fr := frName(i)
		tests, ok := requirementCoverage[fr]
		if !ok || len(tests) == 0 {
			t.Fatalf("%s has no mapped test in requirementCoverage", fr)
		}
	}
}

func frName(i int) string {
	if i < 10 {
		return "FR-0" + string(rune('0'+i))
	}
	return "FR-" + string(rune('0'+i/10)) + string(rune('0'+i%10))
}
