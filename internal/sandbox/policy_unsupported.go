//go:build !darwin && !linux

package sandbox

import "fmt"

func readPolicy(string) ([]byte, error) {
	return nil, fmt.Errorf("sandbox policy platform unsupported")
}
func readMetadata(string) ([]byte, error) {
	return nil, fmt.Errorf("Docker metadata platform unsupported")
}
