package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"jig/internal/datastore"
	"jig/internal/review"
	"jig/internal/step"
	"jig/internal/workflow"
)

const (
	maxReviewDocument = 256 * 1024
	maxReviewRound    = 1024 * 1024
)

func (s *scheduler) dispatchReview(st *workflow.Step) {
	sess, err := s.prepareReview(st)
	if err != nil {
		state := s.states[st.ID]
		state.Result = &step.Result{Status: step.StatusFailed, Err: err.Error()}
		s.applyFailurePolicy(st.ID, st)
		return
	}
	s.reviewSessions[st.ID] = sess
	s.transition(st.ID, s.states[st.ID].Status, step.StatusAwaitingReview)
	s.emit(ReviewRequest{
		RunID: s.runID, StepID: st.ID, RoundID: sess.RoundID,
		Choices: reviewChoices(st), Documents: sess.Documents,
		DraftPath: datastore.ReviewDraftPath(s.runDir, st.ID, sess.RoundID),
		Diff: func() string {
			for _, d := range sess.Documents {
				if d.Format == "diff" {
					return d.Content
				}
			}
			return ""
		}(),
	})
}

func (s *scheduler) prepareReview(st *workflow.Step) (review.Session, error) {
	roundID := fmt.Sprintf("g%03d-i%03d", s.states[st.ID].Generation, s.states[st.ID].Iteration)
	var docs []review.Document
	total := 0
	for i, target := range st.Review {
		content, err := s.reviewContent(st.ID, target)
		if err != nil {
			return review.Session{}, fmt.Errorf("review %q: %w", st.ID, err)
		}
		if len(content) > maxReviewDocument {
			return review.Session{}, fmt.Errorf("review document %q exceeds %d bytes", target.Label, maxReviewDocument)
		}
		if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
			return review.Session{}, fmt.Errorf("review document %q is binary or contains NUL", target.Label)
		}
		total += len(content)
		if total > maxReviewRound {
			return review.Session{}, fmt.Errorf("review round exceeds %d bytes", maxReviewRound)
		}
		format := "text"
		if target.Source == "diff" {
			format = "diff"
		} else if strings.HasSuffix(strings.ToLower(target.Source), ".md") || strings.HasPrefix(target.Source, "@") {
			format = "markdown"
		}
		id := fmt.Sprintf("%02d-%s", i+1, sanitizeReviewName(target.Label))
		d := review.Document{ID: id, Label: strings.TrimSpace(target.Label), Source: target.Source, Format: format, SHA256: review.Digest(content), LineCount: len(strings.Split(content, "\n")), Content: content}
		if dir := datastore.ReviewDocumentsDir(s.runDir, st.ID, roundID); dir != "" {
			path := filepath.Join(dir, id+reviewExtension(format))
			d.SnapshotPath = path
			if existing, readErr := os.ReadFile(path); readErr == nil {
				if string(existing) != content {
					return review.Session{}, fmt.Errorf("review snapshot %q conflicts with existing round", id)
				}
			} else if os.IsNotExist(readErr) {
				if err := review.WriteSnapshot(path, content); err != nil {
					return review.Session{}, err
				}
			} else {
				return review.Session{}, readErr
			}
		}
		docs = append(docs, d)
	}
	return review.Session{StepID: st.ID, RoundID: roundID, Documents: docs}, nil
}

func (s *scheduler) reviewContent(stepID string, target workflow.ReviewTarget) (string, error) {
	source := target.Source
	if source == "diff" {
		return s.collectDepDiffs(stepID), nil
	}
	if strings.HasPrefix(source, "@") {
		ref := strings.TrimPrefix(source, "@")
		parts := strings.Split(ref, ".")
		state := s.states[parts[0]]
		if state == nil || state.Result == nil {
			return "", fmt.Errorf("source %q has no result", source)
		}
		if len(parts) == 1 {
			if state.Result.OutputPath == "" {
				return "", fmt.Errorf("source %q has no output", source)
			}
			data, err := os.ReadFile(state.Result.OutputPath)
			return string(data), err
		}
		m := s.structured[parts[0]]
		if m == nil && len(state.Result.Structured) > 0 {
			_ = json.Unmarshal(state.Result.Structured, &m)
		}
		var value any = m
		for _, part := range parts[1:] {
			obj, ok := value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("source %q is not text", source)
			}
			value = obj[part]
		}
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("source %q is not text", source)
		}
		return text, nil
	}
	path := target.ResolvedPath()
	if path == "" {
		path = source
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func sanitizeReviewName(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
func reviewExtension(format string) string {
	if format == "diff" {
		return ".diff"
	}
	if format == "markdown" {
		return ".md"
	}
	return ".txt"
}
func (s *scheduler) finalizeReview(id string, sub review.Submission) {
	sess, ok := s.reviewSessions[id]
	if !ok {
		s.emit(RunError{RunID: s.runID, Err: fmt.Sprintf("review %q has no active session", id)})
		return
	}
	st := s.stepByID(id)
	if sub.StepID == "" {
		sub.StepID = id
	}
	if sub.RoundID == "" {
		sub.RoundID = sess.RoundID
	}
	// Resolve is kept as a small source-compatible adapter for callers that only
	// have a verdict; the workspace API always supplies these fields explicitly.
	if len(sub.Reviewed) == 0 && len(sub.Documents) == 0 {
		for _, d := range sess.Documents {
			sub.Documents = append(sub.Documents, d.Record())
			sub.Reviewed = append(sub.Reviewed, d.ID)
		}
	}
	if err := review.ValidateSubmission(sess, sub, reviewChoices(st)); err != nil {
		s.emit(RunError{RunID: s.runID, Err: err.Error()})
		return
	}
	sub.SchemaVersion = 1
	feedback := review.RenderFeedback(sess, sub)
	if path := datastore.ReviewSubmissionPath(s.runDir, id, sess.RoundID); path != "" {
		if err := review.WriteSubmission(path, sub); err != nil {
			s.emit(RunError{RunID: s.runID, Err: err.Error()})
			return
		}
		if err := review.WriteFeedback(datastore.ReviewFeedbackPath(s.runDir, id, sess.RoundID), feedback); err != nil {
			s.emit(RunError{RunID: s.runID, Err: err.Error()})
			return
		}
		_ = review.WriteSubmission(datastore.OutputJSONPath(s.runDir, id), sub)
		_ = review.WriteFeedback(datastore.OutputPath(s.runDir, id), feedback)
	}
	state := s.states[id]
	state.Result = &step.Result{Status: step.StatusSucceeded, Verdict: sub.Verdict, OutputPath: datastore.OutputPath(s.runDir, id)}
	s.emit(ReviewSubmitted{RunID: s.runID, StepID: id, RoundID: sess.RoundID, Verdict: sub.Verdict, CommentCount: len(sub.Comments)})
	s.transition(id, step.StatusAwaitingReview, step.StatusSucceeded)
	delete(s.reviewSessions, id)
	if st.Loop != nil {
		s.recordLoopIntentWithFeedback(id, st, feedback)
	}
}
