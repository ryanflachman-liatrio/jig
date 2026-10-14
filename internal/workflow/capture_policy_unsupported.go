//go:build !darwin && !linux

package workflow

import "os"

const captureFileLimit = 1 << 20

type captureLedger struct {
	read, unique, bindings, links, visited int
	blobs                                  map[[32]byte][]byte
}
type captureCapture struct {
	bytes      []byte
	digest     [32]byte
	executable bool
	source     string
}

func captureValidatePath(string) error                     { return CapturePlatform }
func capturePin(string) (*os.File, error)                  { return nil, CapturePlatform }
func captureSelectBundle(string, string) (*os.File, error) { return nil, CapturePlatform }
func (*captureLedger) capture(*os.File, string, func()) (captureCapture, error) {
	return captureCapture{}, CapturePlatform
}
func (*captureLedger) bundle(*os.File) (map[string]captureCapture, error) {
	return nil, CapturePlatform
}
