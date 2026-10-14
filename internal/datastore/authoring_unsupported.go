//go:build !darwin && !linux

package datastore

func ReadAuthoringFile(string, string, int64) ([]byte, error) { return nil, ErrAuthoringStorage }
