package notification

import (
	"encoding/json"
	"io"
)

// jsonEncoder wraps encoding/json in a way that keeps the calling code out of
// the standard-library import path so payload/senders share a tiny helper for
// tests and production alike.
func jsonEncoder(w io.Writer) *json.Encoder { return json.NewEncoder(w) }
