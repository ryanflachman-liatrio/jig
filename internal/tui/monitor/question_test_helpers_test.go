package monitor

import (
	"jig/internal/engine"
	"jig/internal/interaction"
)

func questionEvent(runID, stepID, requestID string, fields ...interaction.QuestionField) engine.AgentQuestion {
	return engine.AgentQuestion{
		RunID:  runID,
		StepID: stepID,
		Request: interaction.QuestionRequest{
			ID:     requestID,
			Fields: fields,
		},
	}
}

func selectQuestion(id, header, prompt string, multi bool, options ...interaction.QuestionOption) interaction.QuestionField {
	kind := interaction.FieldSingleSelect
	if multi {
		kind = interaction.FieldMultiSelect
	}
	return interaction.QuestionField{
		ID:          id,
		Header:      header,
		Prompt:      prompt,
		Kind:        kind,
		Options:     options,
		AllowCustom: false,
	}
}
