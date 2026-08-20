package interaction

import (
	"fmt"
	"strings"
)

type FieldKind string

const (
	FieldText         FieldKind = "text"
	FieldSingleSelect FieldKind = "single_select"
	FieldMultiSelect  FieldKind = "multi_select"
)

type QuestionRequest struct {
	ID      string
	Message string
	Fields  []QuestionField
}

type QuestionField struct {
	ID          string
	Header      string
	Prompt      string
	Description string
	Kind        FieldKind
	Options     []QuestionOption
	Required    bool
	AllowCustom bool
}

type QuestionOption struct {
	Value       string
	Label       string
	Description string
}

type ResponseAction string

const (
	ActionAccept  ResponseAction = "accept"
	ActionDecline ResponseAction = "decline"
	ActionCancel  ResponseAction = "cancel"
)

type QuestionResponse struct {
	RequestID string
	Action    ResponseAction
	Answers   map[string]Answer
}

type Answer struct {
	Values []string
	Custom string
}

func (r QuestionRequest) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("question request id is required")
	}
	if len(r.Fields) == 0 {
		return fmt.Errorf("question request must contain at least one field")
	}
	seen := make(map[string]struct{}, len(r.Fields))
	for i, field := range r.Fields {
		if strings.TrimSpace(field.ID) == "" {
			return fmt.Errorf("question field %d: id is required", i)
		}
		if _, ok := seen[field.ID]; ok {
			return fmt.Errorf("question field %q is duplicated", field.ID)
		}
		seen[field.ID] = struct{}{}
		if strings.TrimSpace(field.Prompt) == "" {
			return fmt.Errorf("question field %q: prompt is required", field.ID)
		}
		switch field.Kind {
		case FieldText:
			if len(field.Options) != 0 {
				return fmt.Errorf("question field %q: text fields cannot have options", field.ID)
			}
			if field.AllowCustom {
				return fmt.Errorf("question field %q: text fields cannot enable custom answers", field.ID)
			}
		case FieldSingleSelect, FieldMultiSelect:
			if len(field.Options) == 0 {
				return fmt.Errorf("question field %q: select fields require options", field.ID)
			}
			values := make(map[string]struct{}, len(field.Options))
			for j, option := range field.Options {
				if strings.TrimSpace(option.Value) == "" {
					return fmt.Errorf("question field %q option %d: value is required", field.ID, j)
				}
				if strings.TrimSpace(option.Label) == "" {
					return fmt.Errorf("question field %q option %d: label is required", field.ID, j)
				}
				if _, ok := values[option.Value]; ok {
					return fmt.Errorf("question field %q option value %q is duplicated", field.ID, option.Value)
				}
				values[option.Value] = struct{}{}
			}
		default:
			return fmt.Errorf("question field %q: unknown kind %q", field.ID, field.Kind)
		}
	}
	return nil
}

func (r QuestionResponse) Validate(req QuestionRequest) error {
	if r.RequestID != req.ID {
		return fmt.Errorf("question response id %q does not match request %q", r.RequestID, req.ID)
	}
	switch r.Action {
	case ActionDecline, ActionCancel:
		if len(r.Answers) != 0 {
			return fmt.Errorf("%s response cannot contain answers", r.Action)
		}
		return nil
	case ActionAccept:
	default:
		return fmt.Errorf("unknown question response action %q", r.Action)
	}

	fields := make(map[string]QuestionField, len(req.Fields))
	for _, field := range req.Fields {
		fields[field.ID] = field
	}
	for id, answer := range r.Answers {
		field, ok := fields[id]
		if !ok {
			return fmt.Errorf("answer references unknown question field %q", id)
		}
		if answer.Custom != "" {
			if field.Kind != FieldText && !field.AllowCustom {
				return fmt.Errorf("question field %q does not allow a custom answer", id)
			}
			if len(answer.Values) != 0 {
				return fmt.Errorf("question field %q cannot contain selections and a custom answer", id)
			}
			continue
		}
		if field.Kind == FieldText {
			if len(answer.Values) > 1 {
				return fmt.Errorf("question field %q accepts one text value", id)
			}
			continue
		}
		if field.Kind == FieldSingleSelect && len(answer.Values) > 1 {
			return fmt.Errorf("question field %q accepts one selection", id)
		}
		allowed := make(map[string]struct{}, len(field.Options))
		for _, option := range field.Options {
			allowed[option.Value] = struct{}{}
		}
		for _, value := range answer.Values {
			if _, ok := allowed[value]; !ok {
				return fmt.Errorf("question field %q has unknown selection %q", id, value)
			}
		}
	}
	for _, field := range req.Fields {
		if !field.Required {
			continue
		}
		answer, ok := r.Answers[field.ID]
		if !ok || (len(answer.Values) == 0 && strings.TrimSpace(answer.Custom) == "") {
			return fmt.Errorf("question field %q requires an answer", field.ID)
		}
	}
	return nil
}
