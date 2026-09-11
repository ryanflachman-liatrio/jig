package runexport

import (
	"bytes"
	"strings"
	"testing"

	"jig/internal/engine"
)

func TestDecodeJournalPrefixTornRecord(t *testing.T) {
	lines := fixtureJournalLines(t)
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	buf.WriteString(`{"seq":999,"kind":"run_error"`) // no trailing newline: torn
	result := decodeJournalPrefix(&buf)
	if result.gap == nil || result.gap.Reason != GapTornRecord {
		t.Fatalf("gap = %+v, want %s", result.gap, GapTornRecord)
	}
	if len(result.records) != len(lines) {
		t.Fatalf("accepted %d records, want the full valid prefix of %d", len(result.records), len(lines))
	}
}

func TestDecodeJournalPrefixStopsAtMalformedRecord(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(fixtureJournalLines(t)[0])
	buf.WriteByte('\n')
	buf.WriteString("not json at all\n")
	buf.WriteString(string(fixtureJournalLines(t)[1]))
	buf.WriteByte('\n')
	result := decodeJournalPrefix(&buf)
	if result.gap == nil || result.gap.Reason != GapMalformedRecord {
		t.Fatalf("gap = %+v, want %s", result.gap, GapMalformedRecord)
	}
	if len(result.records) != 1 {
		t.Fatalf("accepted %d records after the malformed line, want exactly the valid prefix (1)", len(result.records))
	}
}

func TestDecodeJournalPrefixOversizedRecordEndsPrefix(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(fixtureJournalLines(t)[0])
	buf.WriteByte('\n')
	buf.WriteString(strings.Repeat("x", maxInputRecord+10))
	buf.WriteByte('\n')
	result := decodeJournalPrefix(&buf)
	if result.gap == nil || result.gap.Reason != GapOversizedRecord {
		t.Fatalf("gap = %+v, want %s", result.gap, GapOversizedRecord)
	}
	if len(result.records) != 1 {
		t.Fatalf("accepted %d records, want exactly the valid prefix (1)", len(result.records))
	}
}

func TestDecodeJournalPrefixUnknownKindPreserved(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"seq":1,"kind":"a_future_event_kind","data":{}}` + "\n")
	result := decodeJournalPrefix(&buf)
	if result.gap != nil {
		t.Fatalf("unexpected gap for a syntactically valid unknown kind: %+v", result.gap)
	}
	if len(result.records) != 1 || result.records[0].event != nil {
		t.Fatalf("records = %+v, want one record with a nil (unknown) event", result.records)
	}
	aliases := newAliasTable()
	anchor := &timeAnchor{}
	rec := projectEvent(result.records[0], aliases, anchor)
	if rec.Kind != "unknown" || rec.Data != nil {
		t.Fatalf("projectEvent(unknown) = %+v, want fixed unknown kind with no data", rec)
	}
}

func TestProjectEventClosedProjectionOmitsRawErrorAndAliasesSteps(t *testing.T) {
	result := decodeJournalPrefix(bytes.NewReader(append(bytes.Join(fixtureJournalLines(t), []byte("\n")), '\n')))
	if result.gap != nil {
		t.Fatalf("unexpected gap decoding the fixture journal: %+v", result.gap)
	}
	aliases := newAliasTable()
	result.seedAliases(aliases)
	anchor := &timeAnchor{}
	for _, r := range result.records {
		if !r.env.Ts.IsZero() {
			anchor.establish(r.env.Ts)
			break
		}
	}
	for _, rec := range result.records {
		out := projectEvent(rec, aliases, anchor)
		if bytes.Contains(out.Data, []byte(syntheticSecrets[0])) {
			t.Fatalf("event %s leaked a secret into its structural projection: %s", out.Kind, out.Data)
		}
		if bytes.Contains(out.Data, []byte("agent-1")) || bytes.Contains(out.Data, []byte("fetch")) {
			t.Fatalf("event %s leaked an original step id: %s", out.Kind, out.Data)
		}
		if sr, ok := rec.event.(engine.StepStatus); ok && sr.Err != "" && bytes.Contains(out.Data, []byte(sr.Err)) {
			t.Fatalf("StepStatus.Err leaked into projection: %s", out.Data)
		}
	}
}

func TestProjectedStatusUnknownEnum(t *testing.T) {
	if got := projectedStatus("some_future_status"); got != "unknown" {
		t.Fatalf("projectedStatus(unrecognized) = %q, want unknown", got)
	}
	if got := projectedStatus("succeeded"); got != "succeeded" {
		t.Fatalf("projectedStatus(succeeded) = %q, want succeeded", got)
	}
}
