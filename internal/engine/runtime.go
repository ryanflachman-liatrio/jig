package engine

import (
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/interaction"
	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

// stepRuntime is the scheduler's in-memory execution state for one step. It
// is owned by the scheduler goroutine like every other scheduler field.
//
// Every field is discarded when an operator reset rewinds the step, unless
// afterReset carries it over. Add new per-step state here rather than as
// another map on scheduler, so reset cannot miss it.
type stepRuntime struct {
	// validation owns an asynchronous post-execution gate until its completion
	// is handled. Its pointer is also the completion's identity token.
	validation *validationWork
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

	// structured caches the JSON decode of the current Result.Structured.
	structured map[string]any
	// diff is the latest worktree diff, refreshed after each execution.
	diff string
	// retryNotBefore delays an automatic retry until its backoff elapses.
	retryNotBefore time.Time

	// skippedByGuard marks a step whose when= condition evaluated false. It is
	// an optional branch: transparent in depsReady, cascadeSkip, and
	// anyPendingRunnable.
	skippedByGuard bool
	// skippedByOperator marks a step a human skipped at the recovery gate
	// (RecoverSkip). It stays StatusFailed but, like on_failure = "continue",
	// does not block its dependents.
	skippedByOperator bool

	// reaped marks a live worker cancelled because a security escalation
	// parked its step. Its completion only records the result: by the time it
	// lands the operator may already have settled the step.
	reaped bool
	// stopping marks a worker cancelled by Run.Stop. Its stepDoneMsg routes to
	// StatusStopped instead of the failure path.
	stopping bool
	// resolvingSeq is the sequence number of an in-flight agent conflict
	// resolution, or 0. A result whose sequence no longer matches was
	// superseded and is dropped.
	resolvingSeq uint64

	// feedback is route feedback injected into the step's next run.
	feedback string
	// rerunSource names the route source that rewound this step (the first
	// contributor in declaration order when rewinds coalesce) and rerunMax its
	// iteration cap, so buildStepContext can say why the step is re-running.
	// In-memory only, like feedback.
	rerunSource string
	rerunMax    int

	// from="user" input collection gathers inputs one at a time before
	// dispatch. preResolvedInputs guards against re-intercepting once all are
	// collected and the step returns to StatusPending.
	pendingInputs     []workflow.Input // remaining prompts
	collectedInputs   []ResolvedInput  // answers so far
	preResolvedInputs []ResolvedInput  // fully collected, ready to inject
	stickyInputs      []ResolvedInput  // once=true answers, kept across rewinds and resets

	// questions are the agent's open questions, keyed by request id.
	questions map[string]pendingQuestion
	// restoredResponses accumulates answers to questions whose worker died.
	// Once the last arrives, the durable session resumes with all of them.
	restoredResponses []interaction.QuestionResponse
}

// rt returns stepID's runtime state, creating it on first use.
func (s *scheduler) rt(stepID string) *stepRuntime {
	r := s.runtime[stepID]
	if r == nil {
		if s.runtime == nil {
			s.runtime = make(map[string]*stepRuntime)
		}
		r = &stepRuntime{}
		s.runtime[stepID] = r
	}
	return r
}

// afterReset returns the state a reset step starts its new generation with.
func (r *stepRuntime) afterReset() *stepRuntime {
	return &stepRuntime{stickyInputs: r.stickyInputs}
}
