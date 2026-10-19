package engine

import "github.com/ryanflachman-liatrio/jig/internal/workflow"

// capacity counts dispatched workers against the global, read-only/mutating,
// and resource-class limits. A fan-out family's local slot is tracked on the
// family itself; acquireSlots and releaseSlots keep the two in step.
type capacity struct {
	inFlight int
	readOnly int
	mutating int
	byClass  map[string]int
}

// admits reports whether st fits under the read-only/mutating and resource
// class limits. The global max_parallel cap is checked by runLoop.
func (c *capacity) admits(st *workflow.Step, d workflow.Defaults) bool {
	if st.Isolation == workflow.IsolationWorktree {
		if d.MaxMutating > 0 && c.mutating >= d.MaxMutating {
			return false
		}
	} else if d.MaxReadOnly > 0 && c.readOnly >= d.MaxReadOnly {
		return false
	}
	if st.ResourceClass == "" {
		return true
	}
	return c.byClass[st.ResourceClass] < d.ResourceLimits[st.ResourceClass]
}

func (c *capacity) add(st *workflow.Step, n int) {
	c.inFlight += n
	if st.Isolation == workflow.IsolationWorktree {
		c.mutating += n
	} else {
		c.readOnly += n
	}
	if st.ResourceClass != "" {
		if c.byClass == nil {
			c.byClass = make(map[string]int)
		}
		c.byClass[st.ResourceClass] += n
	}
}

// acquireSlots charges st's worker to every limit it counts against,
// including its fan-out family's local slot.
func (s *scheduler) acquireSlots(st *workflow.Step) {
	s.slots.add(st, 1)
	if fam := s.familyOf(st.ID); fam != nil {
		fam.InFlight++
	}
}

// releaseSlots undoes acquireSlots. Every path that ends a dispatched worker,
// including failures before the worker starts, must call it exactly once.
func (s *scheduler) releaseSlots(st *workflow.Step) {
	s.slots.add(st, -1)
	if fam := s.familyOf(st.ID); fam != nil {
		fam.InFlight--
	}
}

func (s *scheduler) familyOf(stepID string) *fanOutFamily {
	if state := s.states[stepID]; state != nil && state.ParentID != "" {
		return s.foreach.families[state.ParentID]
	}
	return nil
}
