package step

import (
	"encoding/hex"
	"fmt"
)

const (
	ContainerInputCount            = 64
	ContainerInputFileBytes  int64 = 128 << 20
	ContainerInputTotalBytes int64 = 256 << 20
)

// FileArtifact describes the bytes accepted at publication. Value semantics
// preserve ownership across result snapshots, replay and worker dispatch.
// Mode contains only read and executable permissions; write/set-ID bits are
// never part of the delivered artifact contract.
type FileArtifact struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

func (f FileArtifact) Validate() error {
	digest, err := hex.DecodeString(f.SHA256)
	if err != nil || len(digest) != 32 || f.Size < 0 || f.Size > ContainerInputFileBytes || (f.Mode != 0444 && f.Mode != 0555) {
		return fmt.Errorf("invalid published file metadata")
	}
	return nil
}
