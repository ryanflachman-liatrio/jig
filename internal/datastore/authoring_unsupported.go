//go:build !darwin && !linux

package datastore

func ReadAuthoringFile(string, string, int64) ([]byte, error) { return nil, ErrAuthoringStorage }

type AuthoringReader struct{}

func OpenAuthoringReader(string) (*AuthoringReader, error)  { return nil, ErrAuthoringStorage }
func (*AuthoringReader) Read(string, int64) ([]byte, error) { return nil, ErrAuthoringStorage }
func (*AuthoringReader) Close() error                       { return nil }
