package tui

import (
	"charm.land/bubbles/v2/textarea"

	"jig/internal/tui/shared"
)

type textareaOption = shared.TextareaOption

func newInputTextarea(placeholder string, width, rows int, opts ...textareaOption) textarea.Model {
	return shared.NewInputTextarea(placeholder, width, rows, opts...)
}

func withoutBorder() textareaOption { return shared.WithoutBorder() }
