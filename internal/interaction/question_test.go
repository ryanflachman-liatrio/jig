package interaction

import (
	"strings"
	"testing"
)

func TestQuestionRequestValidate(t *testing.T) {
	valid := QuestionRequest{
		ID: "req-1",
		Fields: []QuestionField{{
			ID:          "format",
			Prompt:      "Choose a format",
			Kind:        FieldSingleSelect,
			Required:    true,
			AllowCustom: true,
			Options: []QuestionOption{
				{Value: "json", Label: "JSON"},
				{Value: "text", Label: "Text"},
			},
		}},
	}
	tests := []struct {
		name string
		req  QuestionRequest
		want string
	}{
		{name: "valid", req: valid},
		{name: "missing request id", req: QuestionRequest{Fields: valid.Fields}, want: "id is required"},
		{name: "no fields", req: QuestionRequest{ID: "req"}, want: "at least one field"},
		{name: "duplicate field", req: QuestionRequest{ID: "req", Fields: []QuestionField{valid.Fields[0], valid.Fields[0]}}, want: "duplicated"},
		{name: "select without options", req: QuestionRequest{ID: "req", Fields: []QuestionField{{ID: "x", Prompt: "X?", Kind: FieldSingleSelect}}}, want: "require options"},
		{name: "duplicate option value", req: QuestionRequest{ID: "req", Fields: []QuestionField{{
			ID: "x", Prompt: "X?", Kind: FieldMultiSelect,
			Options: []QuestionOption{{Value: "a", Label: "A"}, {Value: "a", Label: "Again"}},
		}}}, want: "duplicated"},
		{name: "unknown kind", req: QuestionRequest{ID: "req", Fields: []QuestionField{{ID: "x", Prompt: "X?", Kind: "number"}}}, want: "unknown kind"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestQuestionResponseValidate(t *testing.T) {
	req := QuestionRequest{
		ID: "req-1",
		Fields: []QuestionField{
			{ID: "name", Prompt: "Name?", Kind: FieldText, Required: true},
			{
				ID: "features", Prompt: "Features?", Kind: FieldMultiSelect, AllowCustom: true,
				Options: []QuestionOption{{Value: "cache", Label: "Cache"}, {Value: "retry", Label: "Retry"}},
			},
		},
	}
	tests := []struct {
		name string
		resp QuestionResponse
		want string
	}{
		{
			name: "valid typed answers",
			resp: QuestionResponse{RequestID: "req-1", Action: ActionAccept, Answers: map[string]Answer{
				"name":     {Values: []string{"Jig"}},
				"features": {Values: []string{"cache", "retry"}},
			}},
		},
		{
			name: "valid custom answer",
			resp: QuestionResponse{RequestID: "req-1", Action: ActionAccept, Answers: map[string]Answer{
				"name":     {Values: []string{"Jig"}},
				"features": {Custom: "Neither"},
			}},
		},
		{name: "decline", resp: QuestionResponse{RequestID: "req-1", Action: ActionDecline}},
		{name: "wrong id", resp: QuestionResponse{RequestID: "other", Action: ActionDecline}, want: "does not match"},
		{name: "missing required", resp: QuestionResponse{RequestID: "req-1", Action: ActionAccept}, want: "requires an answer"},
		{name: "unknown field", resp: QuestionResponse{RequestID: "req-1", Action: ActionAccept, Answers: map[string]Answer{
			"name": {Values: []string{"Jig"}},
			"nope": {Values: []string{"x"}},
		}}, want: "unknown question field"},
		{name: "unknown option", resp: QuestionResponse{RequestID: "req-1", Action: ActionAccept, Answers: map[string]Answer{
			"name":     {Values: []string{"Jig"}},
			"features": {Values: []string{"missing"}},
		}}, want: "unknown selection"},
		{name: "answers on cancel", resp: QuestionResponse{RequestID: "req-1", Action: ActionCancel, Answers: map[string]Answer{
			"name": {Values: []string{"Jig"}},
		}}, want: "cannot contain answers"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.resp.Validate(req)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
