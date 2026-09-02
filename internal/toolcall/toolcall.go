// Package toolcall defines the backend-neutral representation of one agent
// tool activity. It is deliberately independent of any protocol SDK so the
// harness, transcript, runner, and monitor can share one durable contract.
package toolcall

import "encoding/json"

// Activity is the complete known state of a tool call. Producers must not
// mutate an Activity after sending it across a harness event boundary.
type Activity struct {
	ID        string          `json:"id,omitempty"`
	Title     string          `json:"title,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Status    string          `json:"status,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Locations []Location      `json:"locations,omitempty"`
	Content   []Content       `json:"content,omitempty"`
}

// Location identifies a file and, when supplied by the backend, a position in
// that file.
type Location struct {
	Path   string `json:"path,omitempty"`
	Line   *int   `json:"line,omitempty"`
	Column *int   `json:"column,omitempty"`
}

// Content is a normalized standard tool-content item. Unknown variants keep
// their original JSON in Raw instead of being discarded.
type Content struct {
	Type string          `json:"type,omitempty"`
	Text string          `json:"text,omitempty"`
	Diff *Diff           `json:"diff,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

// Diff is an adapter-provided snapshot of a file modification. A nil OldText
// means the file did not exist before the change; an empty pointed-to value is
// an existing empty file.
type Diff struct {
	Path    string  `json:"path,omitempty"`
	OldText *string `json:"old_text,omitempty"`
	NewText string  `json:"new_text,omitempty"`
}

// Clone returns an independent copy suitable for crossing ownership
// boundaries or retaining in a transcript block.
func (a *Activity) Clone() *Activity {
	if a == nil {
		return nil
	}
	out := *a
	out.Input = cloneRaw(a.Input)
	out.Output = cloneRaw(a.Output)
	out.Locations = append([]Location(nil), a.Locations...)
	for i := range out.Locations {
		if a.Locations[i].Line != nil {
			v := *a.Locations[i].Line
			out.Locations[i].Line = &v
		}
		if a.Locations[i].Column != nil {
			v := *a.Locations[i].Column
			out.Locations[i].Column = &v
		}
	}
	out.Content = append([]Content(nil), a.Content...)
	for i := range out.Content {
		out.Content[i].Raw = cloneRaw(a.Content[i].Raw)
		if a.Content[i].Diff != nil {
			d := *a.Content[i].Diff
			if d.OldText != nil {
				v := *d.OldText
				d.OldText = &v
			}
			out.Content[i].Diff = &d
		}
	}
	return &out
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

// IsEdit reports whether standard ACP data identifies this as an edit.
func (a *Activity) IsEdit() bool {
	if a == nil {
		return false
	}
	if a.Kind == "edit" {
		return true
	}
	for _, content := range a.Content {
		if content.Diff != nil {
			return true
		}
	}
	return false
}
