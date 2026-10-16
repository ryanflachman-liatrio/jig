package headless_test

import (
	"errors"
	"testing"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/headless"
)

func TestD04ReopenPolicyPrecedence(t *testing.T) {
	for _, action := range []string{"abort", "skip", "resume"} {
		t.Run(action, func(t *testing.T) {
			p := &headless.Policy{OnRecovery: action}
			err := p.Handle(nil, engine.SandboxPolicyBlocked{RunID: "r", Epoch: 1, Reason: "sandbox_policy_denied"})
			var b engine.SandboxPolicyBlocked
			var g *headless.GateError
			if !errors.As(err, &b) || errors.As(err, &g) {
				t.Fatalf("wrong failure classification %v", err)
			}
			if err := p.Handle(nil, engine.ReviewRequest{RunID: "r", StepID: "review"}); err != nil {
				t.Fatal("underlying gate bypassed block")
			}
		})
	}
	p := &headless.Policy{}
	var gate *headless.GateError
	if !errors.As(p.Handle(nil, engine.ReviewRequest{RunID: "r"}), &gate) {
		t.Fatal("unrelated human gate lost exit3 classification")
	}
}
