package harness

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"jig/internal/interaction"
)

func TestACPAskUserQuestionIntegration(t *testing.T) {
	if os.Getenv("JIG_ACP_QUESTION_INTEGRATION") == "" {
		t.Skip("set JIG_ACP_QUESTION_INTEGRATION=1 to run the live adapter test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var asked atomic.Bool
	session, err := NewAcpHarness().Open(ctx, SessionSpec{
		Cwd:          t.TempDir(),
		Prompt:       "Use AskUserQuestion to ask exactly one single-select question with choices Alpha and Beta. After the answer, reply with one short sentence.",
		AllowedTools: []string{"AskUserQuestion"},
		Question: func(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
			asked.Store(true)
			field := req.Fields[0]
			return interaction.QuestionResponse{
				RequestID: req.ID,
				Action:    interaction.ActionAccept,
				Answers: map[string]interaction.Answer{
					field.ID: {Values: []string{field.Options[0].Value}},
				},
			}
		},
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer session.Close()

	for event := range session.Messages() {
		if event.Type == EventResult && event.IsError {
			t.Fatalf("adapter result error: %s", event.ErrText)
		}
	}
	if !asked.Load() {
		t.Fatal("agent completed without creating an elicitation")
	}
}
