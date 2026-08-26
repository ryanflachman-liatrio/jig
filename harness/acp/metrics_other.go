//go:build !darwin

package acp

import "time"

func sampleResources(rootPID int) ResourceSnapshot {
	return ResourceSnapshot{Timestamp: time.Now().UTC(), RootPID: rootPID, CaptureError: "resource sampling is only implemented on macOS"}
}
