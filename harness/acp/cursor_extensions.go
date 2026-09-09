package acp

import (
	"context"
	"encoding/json"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"
)

type CursorQuestion struct {
	ID            string                 `json:"id"`
	Question      string                 `json:"question"`
	Prompt        string                 `json:"prompt"`
	AllowMultiple bool                   `json:"allowMultiple"`
	Options       []CursorQuestionOption `json:"options"`
}
type CursorQuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}
type CursorQuestionRequest struct {
	ToolCallID string           `json:"toolCallId"`
	Title      string           `json:"title"`
	Questions  []CursorQuestion `json:"questions"`
}
type CursorQuestionAnswer struct {
	QuestionID string   `json:"questionId"`
	OptionIDs  []string `json:"optionIds"`
}
type CursorQuestionResponse struct {
	Outcome string                 `json:"outcome"`
	Answers []CursorQuestionAnswer `json:"answers,omitempty"`
	Message string                 `json:"message,omitempty"`
}
type CursorQuestionHandler func(context.Context, CursorQuestionRequest) (CursorQuestionResponse, error)
type CursorPlanHandler func(context.Context, json.RawMessage) (any, error)

func (c *Client) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "_cursor/ask_question":
		var req CursorQuestionRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acpsdk.NewInvalidParams(map[string]string{"error": fmt.Sprintf("cursor question: %v", err)})
		}
		if c.CursorQuestion == nil {
			return CursorQuestionResponse{Outcome: "skipped", Message: "questions are disabled for this step"}, nil
		}
		return c.CursorQuestion(ctx, req)
	case "_cursor/create_plan":
		if c.CursorPlan != nil {
			return c.CursorPlan(ctx, params)
		}
		return map[string]string{"outcome": "rejected", "message": "Cursor plan approval is unsupported in jig"}, nil
	default:
		return nil, acpsdk.NewMethodNotFound(method)
	}
}
