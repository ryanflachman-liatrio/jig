package step

import "fmt"

// ContainerPhase is private local ownership evidence, never a public telemetry
// label. A phase intent fixes identity before the runtime may create anything.
type ContainerPhase struct {
	RunID       string         `json:"run_id"`
	StepID      string         `json:"step_id"`
	Generation  int            `json:"generation"`
	Iteration   int            `json:"iteration"`
	Attempt     int            `json:"attempt"`
	Nonce       string         `json:"nonce"`
	Phase       string         `json:"phase"` // main or validation
	Name        string         `json:"name"`
	DaemonID    string         `json:"daemon_id"`
	EndpointID  string         `json:"endpoint_id"` // pinned endpoint fingerprint
	ImageID     string         `json:"image_id"`
	ContainerID string         `json:"container_id,omitempty"`
	State       ContainerState `json:"state"`
}

type ContainerState string

const (
	ContainerIntent      ContainerState = "intent"
	ContainerCreated     ContainerState = "created"
	ContainerStopped     ContainerState = "stopped"
	ContainerUncertain   ContainerState = "uncertain"
	ContainerRemoved     ContainerState = "removed"
	ContainerAbsent      ContainerState = "absent"
	ContainerCleanupDebt ContainerState = "cleanup_debt"
)

// ContainerLedger is bounded run-owned phase history. Removed/absent records
// remain as tombstones so a nonce cannot be reused after cleanup.
type ContainerLedger map[string]ContainerPhase

const MaxContainerPhases = 4096

func (p ContainerPhase) Key() string { return p.Nonce + "/" + p.Phase }
func (p ContainerPhase) Quiescent() bool {
	return p.State == ContainerStopped || p.State == ContainerRemoved || p.State == ContainerAbsent || p.State == ContainerCleanupDebt
}
func (p ContainerPhase) Retained() bool {
	return p.State != ContainerRemoved && p.State != ContainerAbsent
}

// Check validates without mutation; callers persist before applying the fact.
func (l ContainerLedger) Check(p ContainerPhase) error {
	for _, s := range []string{p.RunID, p.StepID, p.Nonce, p.Name, p.DaemonID, p.EndpointID, p.ImageID} {
		if len(s) == 0 || len(s) > 256 {
			return fmt.Errorf("invalid container ownership identity")
		}
	}
	if len(p.Nonce) != 32 || p.Generation < 0 || p.Iteration < 0 || p.Attempt < 0 || (p.Phase != "main" && p.Phase != "validation") || len(p.ContainerID) > 256 {
		return fmt.Errorf("invalid container execution coordinates")
	}
	for _, r := range p.Nonce {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return fmt.Errorf("invalid execution nonce")
		}
	}
	old, exists := l[p.Key()]
	if !exists {
		if len(l) >= MaxContainerPhases {
			return fmt.Errorf("container ownership history limit exceeded")
		}
		if p.State != ContainerIntent || p.ContainerID != "" {
			return fmt.Errorf("container phase must begin with intent")
		}
		mainFound := false
		for _, other := range l {
			if other.RunID != p.RunID || other.Name == p.Name {
				return fmt.Errorf("conflicting container ownership identity")
			}
			if other.StepID == p.StepID && !other.Quiescent() {
				return fmt.Errorf("previous container termination unconfirmed")
			}
			if other.Nonce == p.Nonce {
				if other.StepID != p.StepID || other.Generation != p.Generation || other.Iteration != p.Iteration || other.Attempt != p.Attempt || other.DaemonID != p.DaemonID || other.EndpointID != p.EndpointID || other.ImageID != p.ImageID {
					return fmt.Errorf("container attempt identity changed between phases")
				}
				mainFound = other.Phase == "main"
			}
		}
		if p.Phase == "validation" && !mainFound {
			return fmt.Errorf("validation has no confirmed main phase")
		}
		return nil
	}
	identity := p
	identity.State = old.State
	identity.ContainerID = old.ContainerID
	if identity != old {
		return fmt.Errorf("container ownership identity changed")
	}
	if old.ContainerID != "" && p.ContainerID != old.ContainerID {
		return fmt.Errorf("container ID changed")
	}
	allowed := false
	switch old.State {
	case ContainerIntent:
		allowed = p.State == ContainerCreated && p.ContainerID != "" || p.State == ContainerAbsent && p.ContainerID == "" || p.State == ContainerUncertain
	case ContainerCreated:
		allowed = p.State == ContainerStopped || p.State == ContainerUncertain
	case ContainerStopped:
		allowed = p.State == ContainerRemoved || p.State == ContainerCleanupDebt
	case ContainerCleanupDebt:
		allowed = p.State == ContainerRemoved
	}
	if !allowed {
		return fmt.Errorf("invalid container lifecycle transition %q to %q", old.State, p.State)
	}
	return nil
}
