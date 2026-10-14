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
