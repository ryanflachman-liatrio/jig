package review

import (
	"fmt"
	"os"
	"strings"

	domain "jig/internal/review"
)

type document struct {
	meta  domain.Document
	lines []string
}

func loadDocuments(session domain.Session) ([]document, error) {
	docs := make([]document, len(session.Documents))
	for i, meta := range session.Documents {
		content := meta.Content
		if meta.SnapshotPath != "" {
			data, err := os.ReadFile(meta.SnapshotPath)
			if err != nil {
				return nil, fmt.Errorf("load review document %q: %w", meta.ID, err)
			}
			content = string(data)
		}
		if domain.Digest(content) != meta.SHA256 {
			return nil, fmt.Errorf("review document %q digest mismatch", meta.ID)
		}
		docs[i] = document{meta: meta, lines: strings.Split(content, "\n")}
	}
	return docs, nil
}

func (d document) anchor(digest string, start, end int) domain.Anchor {
	if start < 1 {
		start = 1
	}
	if end > len(d.lines) {
		end = len(d.lines)
	}
	quote := strings.Join(d.lines[start-1:end], "\n")
	var prefix, suffix string
	if start > 1 {
		prefix = d.lines[start-2]
	}
	if end < len(d.lines) {
		suffix = d.lines[end]
	}
	return domain.Anchor{DocumentID: d.meta.ID, SHA256: digest, StartLine: start, EndLine: end, Quote: quote, Prefix: prefix, Suffix: suffix}
}
