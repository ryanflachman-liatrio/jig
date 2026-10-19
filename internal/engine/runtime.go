package engine

// stepRuntime is the scheduler's in-memory execution state for one step. It
// is owned by the scheduler goroutine like every other scheduler field.
//
// Every field is discarded when an operator reset rewinds the step, unless
// afterReset carries it over. Add new per-step state here rather than as
// another map on scheduler, so reset cannot miss it.
type stepRuntime struct {
	// resumeSession and message continue a backend session on the next
	// dispatch: recovery resume, agent input, or a restored question.
	resumeSession string
	message       string
	// sessionResumeInFlight marks a dispatch that used resumeSession. A
	// failure then re-parks with CanResume=false (Spec 20 D19).
	sessionResumeInFlight bool
	// failedSessionResume forces CanResume=false after a dead-session resume.
	failedSessionResume bool

	// recoverRounds bounds the recovery loop (maxRecoverRounds).
	recoverRounds int
	// inputRounds bounds agent input delivery (maxInputRounds).
	inputRounds int
}

// rt returns stepID's runtime state, creating it on first use.
func (s *scheduler) rt(stepID string) *stepRuntime {
	r := s.runtime[stepID]
	if r == nil {
		r = &stepRuntime{}
		s.runtime[stepID] = r
	}
	return r
}

// afterReset returns the state a reset step starts its new generation with.
func (r *stepRuntime) afterReset() *stepRuntime {
	return &stepRuntime{}
}
