package engine

import (
	"bufio"
	"os"

	"jig/internal/datastore"
)

// ReplayJournal reads runDir's journal.jsonl and returns the events it recorded,
// in seq order. It is the read side of the "state = fold(journal)" invariant:
// the engine journals every event before fan-out (see internal/manifest), so
// folding the returned events through the same handlers a live run drives
// reconstructs that run's state. This is how the TUI rebuilds its run list — and
// a per-run monitor — for runs from earlier sessions, where no in-memory Run
// handle exists to Snapshot().
//
// A missing journal yields nil with no error: an empty run, or one recorded with
// persistence off. Lines that fail to decode are skipped rather than aborting the
// replay — a torn tail from a crash mid-write, or a kind emitted by a newer
// schema, still leaves every decodable event intact. The caller therefore always
// gets the best reconstruction the journal supports.
func ReplayJournal(runDir string) ([]Event, error) {
	f, err := os.Open(datastore.JournalPath(runDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadString('\n')
		if len(line) > 0 {
			if _, e, err := UnmarshalEnvelope([]byte(line)); err == nil {
				events = append(events, e)
			}
		}
		if readErr != nil {
			break // io.EOF or a read error; either way, stop with what we have.
		}
	}
	return events, nil
}
