//go:build !unix

package runner

import (
	"fmt"
	"os"
)

func openValidationOutput(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("validation output must be regular")
	}
	return os.Open(path)
}

func openContainerInput(root *os.Root, name string) (*os.File, error) {
	return nil, fmt.Errorf("container file inputs unsupported on this platform")
}

func singleLinkFile(info os.FileInfo) bool { return false }
