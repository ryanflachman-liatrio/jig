package monitor

import (
	"testing"

	"jig/internal/engine"
	"jig/internal/interaction"
	"jig/internal/tui/shared"
)

// findGoToHomeExtra locates the "Go to Home" direct-execution palette entry
// across every section returned by PaletteSections, failing the test if it
// is missing — it must be present regardless of focus/gate state (FR U2).
func findGoToHomeExtra(t *testing.T, sections []shared.HelpSection) shared.PaletteExtra {
	t.Helper()
	for _, sec := range sections {
		for _, ex := range sec.Extras {
			if ex.Title == "Go to Home" {
				return ex
			}
		}
	}
	t.Fatal("\"Go to Home\" palette extra not found in PaletteSections()")
	return shared.PaletteExtra{}
}

func TestGoToHomeExtraFromStepsFocus(t *testing.T) {
	m := newMonitorWithSteps(t)
	extra := findGoToHomeExtra(t, m.PaletteSections())
	if _, ok := extra.Run()().(ShowHomeMsg); !ok {
		t.Fatalf("Go to Home from Steps focus = %T, want ShowHomeMsg", extra.Run()())
	}
}

func TestGoToHomeExtraFromTranscriptFocus(t *testing.T) {
	m := newMonitorWithSteps(t)
	m.focus = focusTranscript
	extra := findGoToHomeExtra(t, m.PaletteSections())
	if _, ok := extra.Run()().(ShowHomeMsg); !ok {
		t.Fatalf("Go to Home from Transcript focus = %T, want ShowHomeMsg", extra.Run()())
	}
}

func TestGoToHomeExtraFromOpenGate(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter")) // opens the review workspace (reviewOpen=true), no dirty compose yet
	extra := findGoToHomeExtra(t, m.PaletteSections())
	if _, ok := extra.Run()().(ShowHomeMsg); !ok {
		t.Fatalf("Go to Home from an open, clean gate = %T, want ShowHomeMsg", extra.Run()())
	}
}

// TestGoToHomeExtraRespectsDirtyComposeConfirm proves "Go to Home" delegates
// to the same leaveMonitor() path esc already uses: an unsaved review compose
// buffer must route through the confirmation gate rather than silently
// leaving (A6), even when the operator has since tabbed away to Transcript.
func TestGoToHomeExtraRespectsDirtyComposeConfirm(t *testing.T) {
	m := monitorWithReviewWorkspace(t)
	m, _ = m.Update(key("enter")) // open the workspace
	m, _ = m.Update(key("c"))     // open the comment composer
	m, _ = m.Update(key("x"))     // type unsaved text — now dirty
	m.focus = focusTranscript

	extra := findGoToHomeExtra(t, m.PaletteSections())
	if _, ok := extra.Run()().(RequestLeaveConfirmMsg); !ok {
		t.Fatalf("Go to Home with dirty compose = %T, want RequestLeaveConfirmMsg", extra.Run()())
	}
}

// sectionByTitle returns the first section with the given title, failing the
// test if none matches.
func sectionByTitle(t *testing.T, sections []shared.HelpSection, title string) shared.HelpSection {
	t.Helper()
	for _, sec := range sections {
		if sec.Title == title {
			return sec
		}
	}
	t.Fatalf("no %q section in PaletteSections(): %+v", title, sections)
	return shared.HelpSection{}
}

// TestMonitorPaletteCatalogParityAcrossStates locks in the palette-catalog
// coverage guarantee (Unit 3): PaletteSections() must return the expected
// section/binding set for every reachable Monitor focus/gate combination, and
// "Go to Home" must always be present regardless of state.
func TestMonitorPaletteCatalogParityAcrossStates(t *testing.T) {
	descs := func(sec shared.HelpSection) []string {
		out := make([]string, len(sec.Bindings))
		for i, b := range sec.Bindings {
			out[i] = b.Help().Desc
		}
		return out
	}
	contains := func(list []string, want string) bool {
		for _, v := range list {
			if v == want {
				return true
			}
		}
		return false
	}

	for _, tc := range []struct {
		name        string
		build       func(t *testing.T) Model
		wantSection string
		wantDesc    string
	}{
		{
			name:        "Steps focus",
			build:       func(t *testing.T) Model { return newMonitorWithSteps(t) },
			wantSection: "Steps",
			wantDesc:    "transcript",
		},
		{
			name: "Transcript focus, chat selection",
			build: func(t *testing.T) Model {
				m := newMonitorWithSteps(t)
				m.focus = focusTranscript
				return m
			},
			wantSection: "Transcript",
			wantDesc:    "steps",
		},
		{
			name: "Transcript focus, file selection",
			build: func(t *testing.T) Model {
				m := newMonitorWithSteps(t)
				m.focus = focusTranscript
				m.selKind = "file"
				return m
			},
			wantSection: "Transcript",
			wantDesc:    "copy file",
		},
		{
			name: "Gate: request",
			build: func(t *testing.T) Model {
				m := newMonitorWithSteps(t)
				m, _ = m.Update(EngineEventMsg{Event: engine.InputRequest{RunID: "run-1", StepID: "a"}})
				m.focus = focusGate
				return m
			},
			wantSection: "Gate",
			wantDesc:    "submit",
		},
		{
			name: "Gate: question",
			build: func(t *testing.T) Model {
				m := newMonitorWithSteps(t)
				m, _ = m.Update(EngineEventMsg{Event: questionEvent(
					"run-1", "a", "tu1",
					selectQuestion("format", "Format", "Which format?", false,
						interaction.QuestionOption{Value: "JSON", Label: "JSON"},
						interaction.QuestionOption{Value: "Text", Label: "Text"},
					),
				)})
				m.focus = focusGate
				return m
			},
			wantSection: "Gate",
			wantDesc:    "select",
		},
		{
			name: "Gate: review, not open",
			build: func(t *testing.T) Model {
				m := newMonitorWithSteps(t)
				m, _ = m.Update(EngineEventMsg{Event: engine.ReviewRequest{
					RunID: "run-1", StepID: "a", Choices: []string{"approve", "revise"},
				}})
				m.focus = focusGate
				return m
			},
			wantSection: "Gate",
			wantDesc:    "decision",
		},
		{
			name: "Gate: review, open workspace",
			build: func(t *testing.T) Model {
				m := monitorWithReviewWorkspace(t)
				m, _ = m.Update(key("enter")) // opens the workspace
				return m
			},
			wantSection: "Gate",
			wantDesc:    "mark reviewed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build(t)
			sections := m.PaletteSections()

			sec := sectionByTitle(t, sections, tc.wantSection)
			if !contains(descs(sec), tc.wantDesc) {
				t.Fatalf("%s section descs = %v, want to contain %q", tc.wantSection, descs(sec), tc.wantDesc)
			}

			// "Go to Home" is always reachable, in every state (FR U2).
			findGoToHomeExtra(t, sections)
		})
	}
}
