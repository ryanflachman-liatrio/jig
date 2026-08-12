package detail

import (
	"charm.land/bubbles/v2/viewport"

	"jig/internal/workflow"
)

// Model is the read-only view of one workflow: its steps, their kinds,
// and the loop/gate structure — plus whether the file passes full validation.
// It runs no agents; it just makes the parsed graph legible.
type Model struct {
	path string
	keys detailKeys

	meta    workflow.Meta
	wf      *workflow.Workflow
	loadErr error
	Loaded  bool // set once the async load completes; root guards resize on this

	vp       viewport.Model
	Ready    bool // set once the viewport is initialised; root guards resize on this
	width    int
	height   int
	vpWidth  int  // viewport inner width; the chart lays out to fit it
	viewMode bool // false = flat step list (default), true = chart
}

func New(path string) Model {
	keys := defaultKeys()
	// Run and Toggle are unavailable until a valid workflow loads; disabling
	// them both stops matching their keys and drops them from the footer.
	keys.Run.SetEnabled(false)
	keys.Toggle.SetEnabled(false)
	return Model{path: path, keys: keys}
}
