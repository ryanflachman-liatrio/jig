package sentinel

import "fmt"

// MonitorFailure contains diagnostic context that is safe to persist. Raw
// provider errors and classifier output remain only in the error chain.
type MonitorFailure struct {
	Backend   string `json:"backend"`
	Model     string `json:"model"`
	Stage     string `json:"stage"`
	ErrorType string `json:"error_type,omitempty"`
	Reason    string `json:"reason"`
	Attempts  int    `json:"attempts"`
	ElapsedMS int64  `json:"elapsed_ms"`
	TimeoutMS int64  `json:"timeout_ms"`
	Summary   string `json:"summary"`
	NextStep  string `json:"next_step"`
	cause     error
}

func NewMonitorFailure(info MonitorFailure, cause error) *MonitorFailure {
	info.Backend = RedactText(info.Backend)
	info.Model = RedactText(info.Model)
	info.cause = cause
	return &info
}

func (f *MonitorFailure) Error() string {
	return fmt.Sprintf("%s (backend=%s model=%s stage=%s reason=%s attempts=%d elapsed=%dms timeout=%dms). %s", f.Summary, f.Backend, f.Model, f.Stage, f.Reason, f.Attempts, f.ElapsedMS, f.TimeoutMS, f.NextStep)
}

func (f *MonitorFailure) Unwrap() error { return f.cause }
