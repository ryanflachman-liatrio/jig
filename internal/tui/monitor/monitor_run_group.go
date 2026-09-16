package monitor

// runGroupSpec adapts groupTranscriptItemRuns to a specific compact-group
// feature (read groups, compact tool groups, ...). eligible decides whether
// an item can join a run and, if so, under which discriminator kind; an item
// only joins the current run when its kind matches and it shares the run's
// execution coordinate (sameExecutionCoordinate). build turns a completed run
// into its wrapped group item. skip is optional: it lets a spec keep some
// completed runs as their bare, un-wrapped items instead (e.g. a solitary
// failed read, which must keep rendering its full inline error detail).
type runGroupSpec struct {
	eligible func(item transcriptItem) (kind string, ok bool)
	skip     func(run []transcriptItem) bool
	build    func(run []transcriptItem) transcriptItem
}

// groupTranscriptItemRuns is a pure, page-local post-correlation pass shared
// by every compact-group feature. It scans items in order, accumulating
// consecutive eligible items that share a run's discriminator kind and
// execution coordinate, then hands each completed run to spec.build (or
// leaves it as bare items when spec.skip says so). Ineligible items flush the
// current run and pass through unchanged, preserving any real boundary
// between runs.
func groupTranscriptItemRuns(items []transcriptItem, spec runGroupSpec) []transcriptItem {
	grouped := make([]transcriptItem, 0, len(items))
	run := make([]transcriptItem, 0, 4)
	var runKind string
	flush := func() {
		if len(run) == 0 {
			return
		}
		if spec.skip != nil && spec.skip(run) {
			grouped = append(grouped, run...)
		} else {
			grouped = append(grouped, spec.build(run))
		}
		run = run[:0]
		runKind = ""
	}

	for _, item := range items {
		kind, eligible := spec.eligible(item)
		if !eligible {
			flush()
			grouped = append(grouped, item)
			continue
		}
		if len(run) > 0 && (kind != runKind || !sameExecutionCoordinate(run[0].coord, item.coord)) {
			flush()
		}
		runKind = kind
		run = append(run, item)
	}
	flush()
	return grouped
}
