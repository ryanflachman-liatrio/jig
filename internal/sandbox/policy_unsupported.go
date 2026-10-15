//go:build !darwin && !linux

package sandbox

import "fmt"

func readPolicy(string) ([]byte, error) {
	return nil, fmt.Errorf("sandbox policy platform unsupported")
}
