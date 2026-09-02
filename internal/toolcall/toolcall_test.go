package toolcall

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestActivityJSONRoundTripAndClone(t *testing.T) {
	line, column := 4, 2
	empty := ""
	in := &Activity{
		ID: "edit-1", Title: "Edit", Kind: "edit", Status: "completed",
		Input: json.RawMessage(`{"path":"a.go"}`), Output: json.RawMessage(`{"ok":true}`),
		Locations: []Location{{Path: "a.go", Line: &line, Column: &column}, {Path: "b.go"}},
		Content: []Content{
			{Type: "diff", Diff: &Diff{Path: "new.go", NewText: "package new\n"}},
			{Type: "diff", Diff: &Diff{Path: "empty.go", OldText: &empty, NewText: "x"}},
			{Type: "future", Raw: json.RawMessage(`{"type":"future","value":1}`)},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got Activity
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, &got) || got.Content[0].Diff.OldText != nil || got.Content[1].Diff.OldText == nil || *got.Content[1].Diff.OldText != "" {
		t.Fatalf("round trip = %#v", got)
	}
	clone := in.Clone()
	clone.Input[0] = '!'
	*clone.Locations[0].Line = 9
	*clone.Content[1].Diff.OldText = "changed"
	if in.Input[0] == '!' || *in.Locations[0].Line != 4 || *in.Content[1].Diff.OldText != "" {
		t.Fatal("Clone aliases mutable data")
	}
	if !in.IsEdit() {
		t.Fatal("IsEdit() = false, want true")
	}
}
