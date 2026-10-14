package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/datastore"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

func TestFrozenValidationSchema(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "persisted"}[persisted], func(t *testing.T) {
			dir := t.TempDir()
			raw := []byte(`{"title":"ORIGINAL","type":"object"}`)
			writeCaptureFixture(t, dir, "validation.json", raw)
			wf, err := workflow.Decode("[workflow]\nname='validation-capture'\nversion='1'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\noutput='result.json'\n[step.validate]\noutput_schema='validation.json'\n", dir)
			if err != nil {
				t.Fatal(err)
			}
			wf, assets, err := workflow.PrepareAuthoring(wf, dir)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := newRunAuthoring(assets)
			if err != nil {
				t.Fatal(err)
			}
			rd := ""
			if persisted {
				rd, err = datastore.RunDir(filepath.Join(dir, ".jig"), "validation")
				if err != nil {
					t.Fatal(err)
				}
				if err := authority.publish(rd, wf, datastore.PublishAuthoring); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(filepath.Join(dir, "validation.json")); err != nil {
				t.Fatal(err)
			}
			if persisted {
				wf, err = loadWorkflowSnapshot(rd)
				if err != nil {
					t.Fatal(err)
				}
				wf, authority, err = loadAuthoringSnapshot(rd)
				if err != nil {
					t.Fatal(err)
				}
			}
			s := &scheduler{authoring: authority, runDir: rd}
			for attempt := 0; attempt < 2; attempt++ {
				got, err := s.validationSchema(&wf.Steps[0])
				if err != nil || string(got) != string(raw) {
					t.Fatalf("attempt=%d raw=%s err=%v", attempt, got, err)
				}
				got[0] = 'X'
				if ok, detail := s.runGate(&wf.Steps[0], dir); !ok {
					t.Fatalf("identity-only gate: %s", detail)
				}
			}
			t.Logf("persisted=%v: deleted source; original frozen identity retained across lookup and snapshot restore; enforcement remains unavailable", persisted)
		})
	}
}

func TestStartValidationCaptureFailure(t *testing.T) {
	for _, ref := range []string{"schema.json", ".env"} {
		t.Run(ref, func(t *testing.T) {
			dir := t.TempDir()
			wf, err := workflow.Decode("[workflow]\nname='validation'\nversion='1'\n[[step]]\nid='ready'\ntype='command'\nrun='true'\n[[step]]\nid='late'\ntype='command'\nrun='true'\noutput='result.json'\n[step.validate]\noutput_schema='"+ref+"'\n", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			m := NewManager(nil, "")
			run, err := m.Start(wf)
			var capture workflow.CaptureError
			if run != nil || !errors.As(err, &capture) || len(m.Runs()) != 0 {
				t.Fatalf("run=%v err=%v", run, err)
			}
		})
	}
}
