package harness

import (
	"context"
	"testing"

	"jig/harness/acp"
	"jig/internal/interaction"
)

func TestCursorQuestionTranslationUsesStableOptionIDs(t *testing.T) {
	in := acp.CursorQuestionRequest{ToolCallID: "call-1", Title: "Choose", Questions: []acp.CursorQuestion{
		{ID: "format", Question: "Format", Options: []acp.CursorQuestionOption{{ID: "json", Label: "JSON"}, {ID: "yaml", Label: "JSON"}}},
		{ID: "features", Question: "Features", AllowMultiple: true, Options: []acp.CursorQuestionOption{{ID: "cache", Label: "Cache"}, {ID: "retry", Label: "Retry"}}},
	}}
	var got interaction.QuestionRequest
	h := newCursorQuestionHandler(func(_ context.Context, req interaction.QuestionRequest) interaction.QuestionResponse {
		got = req
		return interaction.QuestionResponse{RequestID: req.ID, Action: interaction.ActionAccept, Answers: map[string]interaction.Answer{"format": {Values: []string{"yaml"}}, "features": {Values: []string{"cache", "retry"}}}}
	})
	out, err := h(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "call-1" || got.Fields[1].Kind != interaction.FieldMultiSelect {
		t.Fatalf("request = %+v", got)
	}
	if out.Outcome != "answered" || out.Answers[0].OptionIDs[0] != "yaml" {
		t.Fatalf("response = %+v", out)
	}
}

func TestCursorQuestionDisabledAndRejectsMissingID(t *testing.T) {
	h := newCursorQuestionHandler(nil)
	out, err := h(context.Background(), acp.CursorQuestionRequest{ToolCallID: "x"})
	if err != nil || out.Outcome != "skipped" {
		t.Fatalf("disabled = %+v, %v", out, err)
	}
	_, err = cursorQuestionRequest(acp.CursorQuestionRequest{Questions: []acp.CursorQuestion{{ID: "q", Question: "Q", Options: []acp.CursorQuestionOption{{ID: "a", Label: "A"}}}}})
	if err == nil {
		t.Fatal("missing toolCallId accepted")
	}
}
