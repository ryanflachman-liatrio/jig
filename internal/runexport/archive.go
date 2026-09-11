package runexport

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Fixed literal archive member names and order (spec: "Archive layout is
// fixed"). transcriptMember is appended only in text mode.
const (
	readmeMember     = "README.md"
	manifestMember   = "manifest.json"
	runMember        = "run.json"
	eventsMember     = "events.jsonl"
	transcriptMember = "transcript.jsonl"
)

// spooledMember is one archive member already rendered to a private
// temporary file, with its length/digest known so the manifest can be built
// before the ZIP itself is written.
type spooledMember struct {
	name string
	path string
	size int64
	sha  string
}

// spoolMember writes data to a new owner-only temporary file in dir and
// returns its recorded length/digest. Every spool is removed by the caller on
// both the success and failure path (spec: "remove every spool on success or
// failure").
func spoolMember(dir, name string, data []byte) (spooledMember, error) {
	f, err := os.CreateTemp(dir, "jig-export-member-*")
	if err != nil {
		return spooledMember{}, fmt.Errorf("%w: create export member spool", ErrOperational)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		os.Remove(f.Name())
		return spooledMember{}, fmt.Errorf("%w: secure export member spool", ErrOperational)
	}
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return spooledMember{}, fmt.Errorf("%w: write export member spool", ErrOperational)
	}
	sum := sha256.Sum256(data)
	return spooledMember{name: name, path: f.Name(), size: int64(len(data)), sha: hex.EncodeToString(sum[:])}, nil
}

func removeSpools(spools []spooledMember) {
	for _, s := range spools {
		_ = os.Remove(s.path)
	}
}

// buildManifest assembles manifest.json from already-spooled member digests
// (excluding the manifest itself) plus the accumulated completeness/privacy
// accounting.
func buildManifest(mode, completeness string, gaps []Gap, counters Counters, members []spooledMember) ([]byte, error) {
	digests := make([]MemberDigest, len(members))
	for i, m := range members {
		digests[i] = MemberDigest{Name: m.name, Bytes: m.size, SHA256: m.sha}
	}
	manifest := Manifest{
		FormatVersion: formatVersion, RedactionPolicyVersion: redactionPolicyVersion,
		ContentMode: mode, Completeness: completeness, Gaps: gaps, Counters: counters, Members: digests,
	}
	return json.MarshalIndent(manifest, "", "  ")
}

// writeArchive spools every member, builds the manifest, and streams the
// literal member order into a private owner-only temporary ZIP in
// destDir. It returns that temp file's path for the caller to publish; the
// caller must remove it on any later failure.
func writeArchive(destDir string, readme, runJSON, eventsJSONL []byte, transcriptJSONL []byte, includeText bool, mode, completeness string, gaps []Gap, counters Counters) (string, error) {
	var spools []spooledMember
	defer func() { removeSpools(spools) }()

	var totalArchiveBytes int64
	add := func(name string, data []byte) error {
		totalArchiveBytes += int64(len(data))
		if totalArchiveBytes > maxArchiveBytes {
			return fmt.Errorf("%w: archive content exceeds the size limit", ErrOperational)
		}
		spool, err := spoolMember(destDir, name, data)
		if err != nil {
			return err
		}
		spools = append(spools, spool)
		return nil
	}
	if err := add(readmeMember, readme); err != nil {
		return "", err
	}
	if err := add(runMember, runJSON); err != nil {
		return "", err
	}
	if err := add(eventsMember, eventsJSONL); err != nil {
		return "", err
	}
	if includeText {
		if err := add(transcriptMember, transcriptJSONL); err != nil {
			return "", err
		}
	}

	manifestData, err := buildManifest(mode, completeness, gaps, counters, spools)
	if err != nil {
		return "", fmt.Errorf("%w: build manifest", ErrOperational)
	}

	tmp, err := os.CreateTemp(destDir, "jig-export-*.zip")
	if err != nil {
		return "", fmt.Errorf("%w: create private temporary archive", ErrOperational)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: secure private temporary archive", ErrOperational)
	}
	zw := zip.NewWriter(tmp)
	writeZipEntry := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	failArchive := func(err error) (string, error) {
		_ = zw.Close()
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: write archive: %v", ErrOperational, err)
	}
	if err := writeZipEntry(manifestMember, manifestData); err != nil {
		return failArchive(err)
	}
	for _, m := range spools {
		data, err := os.ReadFile(m.path)
		if err != nil {
			return failArchive(err)
		}
		if err := writeZipEntry(m.name, data); err != nil {
			return failArchive(err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: close archive", ErrOperational)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("%w: close archive", ErrOperational)
	}
	return tmp.Name(), nil
}

// publish links tmpPath to destination without ever replacing a competing
// file created after validation (spec FR-06): os.Link fails closed with
// ErrExist if destination now exists, unlike os.Rename which would silently
// replace it. The temporary file is removed in every case.
func publish(tmpPath, destination string) error {
	defer os.Remove(tmpPath)
	if err := os.Link(tmpPath, destination); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: destination was created concurrently", ErrOperational)
		}
		return fmt.Errorf("%w: publish archive", ErrOperational)
	}
	return nil
}

// renderReadme generates the recipient-facing summary from validated,
// alias-only data. No source text, path, or prose is ever interpolated.
func renderReadme(mode string, run RunSummary, gaps []Gap, includeText bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# jig run export: %s\n\n", run.RunAlias)
	fmt.Fprintf(&b, "- Content mode: `%s`\n", mode)
	fmt.Fprintf(&b, "- Observed state: `%s` (authoritative: %t)\n", run.State, run.StateAuthoritative)
	if len(gaps) > 0 {
		fmt.Fprintf(&b, "- Completeness: **partial** — %d evidence gap(s) below\n", len(gaps))
	} else {
		b.WriteString("- Completeness: **complete**\n")
	}
	b.WriteString("\nThis archive contains no prompts, code, tool payloads, or raw identifiers by default. ")
	b.WriteString("It was generated entirely offline by `jig export` and requires no jig installation, backend login, or network access to read.\n\n")

	b.WriteString("## Steps\n\n")
	b.WriteString("| Step | Type | Status | Attempt | Iteration |\n|---|---|---|---|---|\n")
	for _, s := range run.Steps {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d |\n", s.Alias, orUnknown(s.Type), s.Status, s.Attempt, s.Iteration)
	}

	if len(gaps) > 0 {
		b.WriteString("\n## Evidence gaps\n\n")
		b.WriteString("| Reason | Source | Step | Line/Count |\n|---|---|---|---|\n")
		for _, g := range gaps {
			fmt.Fprintf(&b, "| %s | %s | %s | %d |\n", g.Reason, g.Source, orDash(g.Alias), g.Line+g.Count)
		}
	}

	b.WriteString("\n## Recipient notes\n\n")
	b.WriteString("- Aliases (`run-1`, `workflow-1`, `step-NNNN`, `tool-NNNN`) replace every original identifier; there is no mapping back to the source run in this archive.\n")
	b.WriteString("- Times are signed integer milliseconds relative to the first observed event, not absolute dates.\n")
	if includeText {
		b.WriteString("- `transcript.jsonl` contains best-effort sanitized conversation text. Sanitization is not a guarantee of anonymity: review before sharing further — it may still identify people, projects, or contain sensitive content.\n")
	} else {
		b.WriteString("- This export does not include conversation text. Re-run with `--include-text` for sanitized transcript diagnostics.\n")
	}
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// destinationTempDir is the directory writeArchive/spoolMember use for
// private staging: always the destination's own parent, already validated by
// resolveRequest to exist and sit outside the run's persistence root.
func destinationTempDir(destination string) string {
	return filepath.Dir(destination)
}
