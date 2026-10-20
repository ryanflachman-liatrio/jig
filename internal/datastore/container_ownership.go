package datastore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ryanflachman-liatrio/jig/internal/step"
)

// checkContainerRetention refuses deletion when ownership is unresolved or
// cleanup debt remains. Corrupt/truncated journals fail closed. It reads only
// the local journal; it never contacts a daemon or reconciles resources.
func checkContainerRetention(runDir string) error {
	f, err := os.Open(JournalPath(runDir))
	if err != nil {
		return err
	}
	defer f.Close()
	ledger := make(step.ContainerLedger)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		var env struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			return fmt.Errorf("invalid ownership journal")
		}
		if env.Kind != "container_lifecycle" {
			continue
		}
		var event struct {
			Ownership step.ContainerPhase `json:"ownership"`
		}
		if err := json.Unmarshal(env.Data, &event); err != nil {
			return fmt.Errorf("invalid ownership record")
		}
		if err := ledger.Check(event.Ownership); err != nil {
			return err
		}
		ledger[event.Ownership.Key()] = event.Ownership
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ownership journal: %w", err)
	}
	for _, p := range ledger {
		if p.Retained() {
			return fmt.Errorf("retained container ownership")
		}
	}
	return nil
}
