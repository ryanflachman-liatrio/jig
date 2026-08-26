package acp

import "time"

// ResourceSnapshot describes the ACP adapter process group at one lifecycle
// boundary. CaptureError is populated instead of failing an agent turn when a
// platform cannot obtain a measurement.
type ResourceSnapshot struct {
	Timestamp         time.Time `json:"timestamp"`
	RootPID           int       `json:"root_pid"`
	ProcessGroupID    int       `json:"process_group_id"`
	ProcessGroupCount int       `json:"process_group_process_count"`
	RSSBytes          uint64    `json:"rss_bytes"`
	OpenFDCount       int       `json:"open_fd_count"`
	CaptureError      string    `json:"capture_error,omitempty"`
}

type ResourceSampler func(rootPID int) ResourceSnapshot
