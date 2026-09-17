// Command askquestion-demo renders the real monitor.Model AskUserQuestion
// gate against a canned QuestionRequest, with no engine or agent CLI
// required — a fast way to eyeball panel changes (docs/specs/27-spec-askuserquestion-ui)
// without wiring a full workflow run.
//
// Usage:
//
//	go run ./cmd/askquestion-demo                 # 3 short select questions (Option B: stacked)
//	go run ./cmd/askquestion-demo -mode=paginated # a text field forces the one-at-a-time fallback (Option A)
//	go run ./cmd/askquestion-demo -mode=long      # one field with 12 options, to see scrolling
//
// Resize the terminal while it's running to see the gate panel re-fit; press
// q to answer-cancel and exit, or ctrl+c to quit at any time.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/tui/monitor"
)

const (
	demoRunID  = "demo-run"
	demoStepID = "demo-step"
)

func main() {
	mode := flag.String("mode", "stacked", "which fixture to show: stacked | paginated | long")
	flag.Parse()

	req, err := buildRequest(*mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	m := monitor.New(demoRunID)
	program := tea.NewProgram(&app{m: m, req: req})
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// app adapts monitor.Model (which returns the concrete Model type from
// Update, not tea.Model) to the tea.Model interface, and seeds the gate with
// the demo AgentQuestion event on the first Init.
type app struct {
	m   monitor.Model
	req interaction.QuestionRequest
}

func (a *app) Init() tea.Cmd {
	return func() tea.Msg {
		return monitor.EngineEventMsg{Event: engine.AgentQuestion{
			RunID:   demoRunID,
			StepID:  demoStepID,
			Request: a.req,
		}}
	}
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "ctrl+c" {
		return a, tea.Quit
	}
	if _, ok := msg.(monitor.EngineEventMsg); ok {
		next, cmd := a.m.Update(msg)
		a.m = next.FocusPendingInput()
		return a, cmd
	}
	next, cmd := a.m.Update(msg)
	a.m = next
	return a, cmd
}

func (a *app) View() tea.View {
	v := tea.NewView(a.m.View())
	v.AltScreen = true
	return v
}

func buildRequest(mode string) (interaction.QuestionRequest, error) {
	switch mode {
	case "stacked":
		// Three short select-kind fields with no free text and no "Other…"
		// option: small enough to render together (Option B).
		return interaction.QuestionRequest{
			ID:      "q-stacked",
			Message: "Confirm rollout plan",
			Fields: []interaction.QuestionField{
				{
					ID: "env", Header: "Environment", Prompt: "Deploy to which environment?",
					Kind: interaction.FieldSingleSelect, Required: true,
					Options: []interaction.QuestionOption{
						{Value: "staging", Label: "Staging"},
						{Value: "prod", Label: "Production"},
						{Value: "canary", Label: "Canary"},
					},
				},
				{
					ID: "checks", Header: "Checks", Prompt: "Run which test suites?",
					Kind: interaction.FieldMultiSelect, Required: true,
					Options: []interaction.QuestionOption{
						{Value: "unit", Label: "Unit"},
						{Value: "integration", Label: "Integration"},
						{Value: "e2e", Label: "E2E"},
					},
				},
				{
					ID: "notify", Header: "Notify", Prompt: "Notify the team on completion?",
					Kind: interaction.FieldSingleSelect, Required: true,
					Options: []interaction.QuestionOption{
						{Value: "yes", Label: "Yes"},
						{Value: "no", Label: "No"},
					},
				},
			},
		}, nil

	case "paginated":
		// A free-text field is never shown in stacked mode, so this request
		// falls back to the one-at-a-time view (Option A) with a final
		// review screen listing all three answers.
		return interaction.QuestionRequest{
			ID:      "q-paginated",
			Message: "Confirm rollout plan",
			Fields: []interaction.QuestionField{
				{ID: "release", Prompt: "Release name?", Kind: interaction.FieldText, Required: true},
				{
					ID: "env", Header: "Environment", Prompt: "Deploy to which environment?",
					Kind: interaction.FieldSingleSelect, Required: true,
					Options: []interaction.QuestionOption{
						{Value: "staging", Label: "Staging"},
						{Value: "prod", Label: "Production"},
					},
				},
				{
					ID: "checks", Header: "Checks", Prompt: "Run which test suites?",
					Kind: interaction.FieldMultiSelect, AllowCustom: true,
					Options: []interaction.QuestionOption{
						{Value: "unit", Label: "Unit"},
						{Value: "e2e", Label: "E2E"},
						{Value: "lint", Label: "Lint"},
					},
				},
			},
		}, nil

	case "long":
		// A single field with more options than maxQuestionOptions, to see
		// the scroll hints (▲/▼ more) within the fixed panel height.
		opts := make([]interaction.QuestionOption, 0, 12)
		regions := []string{
			"us-west-1", "us-west-2", "us-east-1", "us-east-2",
			"eu-west-1", "eu-central-1", "ap-southeast-1", "ap-southeast-2",
			"ap-northeast-1", "sa-east-1", "ca-central-1", "af-south-1",
		}
		for _, r := range regions {
			opts = append(opts, interaction.QuestionOption{Value: r, Label: r})
		}
		return interaction.QuestionRequest{
			ID:      "q-long",
			Message: "Confirm rollout plan",
			Fields: []interaction.QuestionField{
				{
					ID: "region", Header: "Region", Prompt: "Which region should we deploy to?",
					Kind: interaction.FieldSingleSelect, Required: true, Options: opts,
				},
			},
		}, nil

	default:
		return interaction.QuestionRequest{}, fmt.Errorf("unknown -mode %q (want stacked, paginated, or long)", mode)
	}
}
