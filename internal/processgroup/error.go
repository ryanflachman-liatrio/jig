package processgroup

// UncertainError fences a workload whose termination could not be confirmed.
// The cause remains inspectable without copying raw process details into UI errors.
type UncertainError struct{ Cause error }

func (e *UncertainError) Error() string { return "managed execution termination uncertain" }
func (e *UncertainError) Unwrap() error { return e.Cause }
