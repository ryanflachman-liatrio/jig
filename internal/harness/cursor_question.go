package harness

import (
	"context"
	"fmt"
	"sync"

	"jig/harness/acp"
	"jig/internal/interaction"
)

// newCursorQuestionHandler keeps the native Cursor protocol isolated from the
// interaction model used by the runner and Gate.
func newCursorQuestionHandler(ask QuestionFn) acp.CursorQuestionHandler {
	var mu sync.Mutex
	active := make(map[string]struct{})
	return func(ctx context.Context, in acp.CursorQuestionRequest) (acp.CursorQuestionResponse, error) {
		if ask == nil {
			return acp.CursorQuestionResponse{Outcome: "skipped", Message: "questions are disabled for this step"}, nil
		}
		req, err := cursorQuestionRequest(in)
		if err != nil {
			return acp.CursorQuestionResponse{}, err
		}
		mu.Lock()
		if _, ok := active[req.ID]; ok {
			mu.Unlock()
			return acp.CursorQuestionResponse{}, fmt.Errorf("duplicate active Cursor question id %q", req.ID)
		}
		active[req.ID] = struct{}{}
		mu.Unlock()
		defer func() { mu.Lock(); delete(active, req.ID); mu.Unlock() }()
		if err := ctx.Err(); err != nil {
			return acp.CursorQuestionResponse{Outcome: "cancelled"}, err
		}
		return encodeCursorQuestionResponse(req, ask(ctx, req))
	}
}

func cursorQuestionRequest(in acp.CursorQuestionRequest) (interaction.QuestionRequest, error) {
	req := interaction.QuestionRequest{ID: in.ToolCallID, Message: in.Title}
	if req.ID == "" {
		return req, fmt.Errorf("Cursor question toolCallId is required")
	}
	for _, question := range in.Questions {
		prompt := question.Prompt
		if prompt == "" {
			prompt = question.Question
		}
		field := interaction.QuestionField{ID: question.ID, Prompt: prompt, Header: question.Question, Required: true, AllowCustom: false, Kind: interaction.FieldSingleSelect}
		if question.AllowMultiple {
			field.Kind = interaction.FieldMultiSelect
		}
		for _, option := range question.Options {
			field.Options = append(field.Options, interaction.QuestionOption{Value: option.ID, Label: option.Label, Description: option.Description})
		}
		req.Fields = append(req.Fields, field)
	}
	if err := req.Validate(); err != nil {
		return req, fmt.Errorf("invalid Cursor question: %w", err)
	}
	return req, nil
}

func encodeCursorQuestionResponse(req interaction.QuestionRequest, response interaction.QuestionResponse) (acp.CursorQuestionResponse, error) {
	if err := response.Validate(req); err != nil {
		return acp.CursorQuestionResponse{}, err
	}
	switch response.Action {
	case interaction.ActionDecline:
		return acp.CursorQuestionResponse{Outcome: "skipped"}, nil
	case interaction.ActionCancel:
		return acp.CursorQuestionResponse{Outcome: "cancelled"}, nil
	case interaction.ActionAccept:
		out := acp.CursorQuestionResponse{Outcome: "answered"}
		for _, field := range req.Fields {
			answer, ok := response.Answers[field.ID]
			if !ok && field.Required {
				return acp.CursorQuestionResponse{}, fmt.Errorf("required Cursor question field %q has no answer", field.ID)
			}
			seen := make(map[string]struct{}, len(answer.Values))
			for _, value := range answer.Values {
				if _, ok := seen[value]; ok {
					return acp.CursorQuestionResponse{}, fmt.Errorf("Cursor question field %q has duplicate selection %q", field.ID, value)
				}
				seen[value] = struct{}{}
			}
			out.Answers = append(out.Answers, acp.CursorQuestionAnswer{QuestionID: field.ID, OptionIDs: append([]string(nil), answer.Values...)})
		}
		return out, nil
	default:
		return acp.CursorQuestionResponse{}, fmt.Errorf("unknown Cursor question response action %q", response.Action)
	}
}
