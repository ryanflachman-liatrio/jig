package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestD05ContainerOutputPredicates(t *testing.T) {
	for _, name := range []string{"binary_exists", "large_text", "contains", "missing_text", "missing_output", "cancelled", "schema_missing"} {
		t.Run(name, func(t *testing.T) {
			w, req, _ := d05Output(t)
			req.Step.Validate = &workflow.Validate{OutputExists: true}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := false
			switch name {
			case "binary_exists", "large_text":
				if err := os.Truncate(filepath.Join(w.Workspace, req.Step.Output), validationOutputLimit+1); err != nil {
					t.Fatal(err)
				}
				want = name == "binary_exists"
				if name == "large_text" {
					req.Step.Validate.OutputContains = "marker"
				}
			case "contains", "missing_text":
				if err := os.WriteFile(filepath.Join(w.Workspace, req.Step.Output), []byte("marker"), 0600); err != nil {
					t.Fatal(err)
				}
				req.Step.Validate.OutputContains = "marker"
				want = true
				if name == "missing_text" {
					req.Step.Validate.OutputContains = "absent"
					want = false
				}
			case "missing_output":
				if err := os.Remove(filepath.Join(w.Workspace, req.Step.Output)); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			case "schema_missing":
				req.Step.Validate.OutputSchema = "schema.json"
			}
			pass, detail, err := validateContainerOutput(ctx, req, w)
			if pass != want {
				t.Fatalf("pass=%v detail=%s err=%v", pass, detail, err)
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name == "schema_missing" && err == nil {
				t.Fatal("missing capture accepted")
			}
		})
	}
}
func TestD05ContainerOutputSchema(t *testing.T) {
	w, req, _ := d05Output(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"const":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	req.Step.Validate = &workflow.Validate{OutputSchema: "schema.json"}
	owned, assets, err := workflow.PrepareAuthoring(&workflow.Workflow{Steps: []workflow.Step{*req.Step}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	req.Step = &owned.Steps[0]
	req.Assets = assets
	req.AssetOwner = req.Step.ID
	if err := os.Remove(filepath.Join(dir, "schema.json")); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{"ok":true}`, `{"ok":false}`, `not json`} {
		if err := os.WriteFile(filepath.Join(w.Workspace, req.Step.Output), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		pass, detail, err := validateContainerOutput(context.Background(), req, w)
		if err != nil || pass != (data == `{"ok":true}`) {
			t.Fatalf("%s: %v %s %v", data, pass, detail, err)
		}
	}
}
