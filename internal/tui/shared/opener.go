package shared

// OpenLocationOwner identifies the Monitor instance and recorded selection.
// Value-only messages cannot be redirected by later navigation or page loads.
type OpenLocationOwner struct {
	Instance                       uint64
	RunID, StepID                  string
	Generation, Iteration, Attempt int
	Seq, Block                     int
}

// OpenLocationTarget is a literal local file path with optional positive,
// one-based coordinates. Zero means no position was supplied.
type OpenLocationTarget struct {
	Path         string
	Line, Column int
}

type OpenLocationRequest struct {
	Owner  OpenLocationOwner
	Target OpenLocationTarget
	// Unavailable is a fixed explanation from selection, never tool text.
	Unavailable string
}

type OpenLocationResult struct {
	ID     uint64
	Owner  OpenLocationOwner
	Notice string
}
