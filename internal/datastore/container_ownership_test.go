package datastore

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

func TestD05ContainerRetention(t *testing.T) {
	for _, mode := range []string{"intent", "created", "stopped", "uncertain", "cleanup_debt", "removed", "absent", "corrupt", "identity change"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now()
			dir := makeRun(t, root, "owned", now.Add(-48*time.Hour))
			p := step.ContainerPhase{RunID: "owned", StepID: "command", Nonce: "0123456789abcdef0123456789abcdef", Phase: "main", Name: "owned-main", DaemonID: "daemon", EndpointID: "endpoint", ImageID: "image", State: step.ContainerIntent}
			f, err := os.OpenFile(JournalPath(dir), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			appendFact := func() {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"kind": "container_lifecycle", "data": map[string]any{"ownership": p}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.Write(append(raw, '\n')); err != nil {
					t.Fatal(err)
				}
			}
			appendFact()
			if mode == "absent" {
				p.State = step.ContainerAbsent
				appendFact()
			} else if mode != "intent" {
				p.State, p.ContainerID = step.ContainerCreated, "id"
				appendFact()
				switch mode {
				case "uncertain":
					p.State = step.ContainerUncertain
					appendFact()
				case "stopped", "cleanup_debt", "removed", "identity change":
					p.State = step.ContainerStopped
					appendFact()
					if mode != "stopped" {
						p.State = step.ContainerState(mode)
						if mode == "identity change" {
							p.State = step.ContainerRemoved
							p.ContainerID = "other"
						}
						appendFact()
					}
				case "corrupt":
					if _, err := f.WriteString("{broken\n"); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			wantDelete := mode == "removed" || mode == "absent"
			candidates, err := Prunable(root, RetentionPolicy{MaxAge: time.Hour}, now)
			if err != nil {
				t.Fatal(err)
			}
			if (len(candidates) == 1) != wantDelete {
				t.Fatalf("candidates %v", candidates)
			}
			deleted, err := Prune(root, RetentionPolicy{MaxAge: time.Hour}, now)
			if err != nil {
				t.Fatal(err)
			}
			if (len(deleted) == 1) != wantDelete {
				t.Fatalf("deleted %v", deleted)
			}
			if exists(t, dir) == wantDelete {
				t.Fatal("wrong on-disk retention")
			}
		})
	}
}
