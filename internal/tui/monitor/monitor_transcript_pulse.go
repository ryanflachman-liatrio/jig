package monitor

import "time"

// pulseFrame selects the running-step thinking pulse's glyph for t (FR-10.4),
// as a pure function of the timestamp: the frame index is
// t.UnixMilli()/100 mod len(frames), quantized to the same 100ms cadence
// monitorFrameInterval already drives. There is no package-level counter or
// second ticker — two calls with the same t always agree.
func pulseFrame(t time.Time, frames []string) string {
	if len(frames) == 0 {
		return ""
	}
	const frameMillis = int64(monitorFrameInterval / time.Millisecond)
	idx := (t.UnixMilli() / frameMillis) % int64(len(frames))
	if idx < 0 {
		idx += int64(len(frames))
	}
	return frames[idx]
}
