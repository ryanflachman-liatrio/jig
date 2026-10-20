package step

import (
	"fmt"
	"testing"
)

func TestD05ContainerLedgerBoundsAndIdentity(t *testing.T) {
	p := ContainerPhase{RunID: "run", StepID: "step", Nonce: "0123456789abcdef0123456789abcdef", Phase: "main", Name: "main", DaemonID: "daemon", EndpointID: "endpoint", ImageID: "image", State: ContainerIntent}
	for _, mode := range []string{"limit", "image drift", "endpoint drift", "daemon drift", "missing main", "replacement while live", "nonce reuse", "empty identity"} {
		t.Run(mode, func(t *testing.T) {
			ledger := make(ContainerLedger)
			next := p
			switch mode {
			case "limit":
				for i := 0; i < MaxContainerPhases; i++ {
					ledger[fmt.Sprint(i)] = p
				}
			case "missing main":
				next.Phase = "validation"
			case "empty identity":
				next.DaemonID = ""
			case "replacement while live":
				ledger[p.Key()] = p
				next.Nonce = "abcdef0123456789abcdef0123456789"
				next.Name = "replacement"
			case "nonce reuse":
				prior := p
				prior.State = ContainerRemoved
				prior.ContainerID = "id"
				ledger[p.Key()] = prior
			default:
				prior := p
				prior.State = ContainerStopped
				prior.ContainerID = "id"
				ledger[p.Key()] = prior
				next.Phase, next.Name = "validation", "validator"
				switch mode {
				case "image drift":
					next.ImageID = "other"
				case "endpoint drift":
					next.EndpointID = "other"
				case "daemon drift":
					next.DaemonID = "other"
				}
			}
			if err := ledger.Check(next); err == nil {
				t.Fatal("invalid ownership accepted")
			}
		})
	}
}
