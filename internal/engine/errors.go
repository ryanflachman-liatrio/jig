package engine

import "errors"

// ErrRunLocked reports that another scheduler holds a run's lease, in this
// process or another one.
var ErrRunLocked = errors.New("run already has a live scheduler")

// ErrRunFinished reports an attempt to reopen a run whose journal records
// run_finished.
var ErrRunFinished = errors.New("run is already finished")

// ErrUnknownJournalKind reports a journal record whose kind this version
// cannot decode. Resume rejects such a journal instead of dropping the record.
var ErrUnknownJournalKind = errors.New("unknown journal event kind")
