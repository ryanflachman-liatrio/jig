package step

import "time"

// Timing measures active execution in the latest dispatch, including validation.
// Human parks freeze the clock; returning to pending starts a new dispatch.
// All timestamps come from the engine, never from the observer's replay clock.
type Timing struct {
	StartedAt   time.Time
	EndedAt     time.Time
	ActiveSince time.Time
	Elapsed     time.Duration
}

// Apply folds a status transition identically for live state and journal replay.
// Missing timestamps remain unknown rather than inventing a duration on reopen.
func (t *Timing) Apply(to Status, at time.Time) {
	if to == StatusPending {
		*t = Timing{}
		return
	}
	if at.IsZero() {
		return
	}
	active := to == StatusRunning || to == StatusValidating
	if active {
		if t.StartedAt.IsZero() {
			t.StartedAt = at
		}
		if t.ActiveSince.IsZero() {
			t.ActiveSince = at
		}
		t.EndedAt = time.Time{}
		return
	}
	t.Pause(at)
	if to == StatusSucceeded || to == StatusFailed || to == StatusSkipped {
		t.EndedAt = at
	}
}

// Pause freezes an active interval at the last known observation. Recovery uses
// this to avoid charging time while no process owned the run.
func (t *Timing) Pause(at time.Time) {
	if !t.ActiveSince.IsZero() {
		if !at.IsZero() && at.After(t.ActiveSince) {
			t.Elapsed += at.Sub(t.ActiveSince)
		}
		t.ActiveSince = time.Time{}
	}
}

// Duration returns elapsed active time at now without mutating the projection.
func (t Timing) Duration(now time.Time) time.Duration {
	d := t.Elapsed
	if !t.ActiveSince.IsZero() && now.After(t.ActiveSince) {
		d += now.Sub(t.ActiveSince)
	}
	return d
}
