//go:build darwin || linux

package datastore

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
)

// AuthoringReader retains the pinned directory for fixed run-owned records.
type AuthoringReader struct{ fd int }

// OpenAuthoringReader pins one run directory for a complete verification pass.
func OpenAuthoringReader(dir string) (*AuthoringReader, error) {
	if dir == "" {
		return nil, ErrAuthoringStorage
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrAuthoringStorage
	}
	return &AuthoringReader{fd: fd}, nil
}
func (r *AuthoringReader) Close() error { err := unix.Close(r.fd); r.fd = -1; return err }

// ReadAuthoringFile rejects symlinks in every component below the run directory.
func ReadAuthoringFile(dir, name string, limit int64) ([]byte, error) {
	r, err := OpenAuthoringReader(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Read(name, limit)
}
func (r *AuthoringReader) Read(name string, limit int64) ([]byte, error) {
	if limit < 0 || (name != "workflow.json" && name != "authoring/manifest.json" && !validBlobName(name)) {
		return nil, ErrAuthoringStorage
	}
	fd, err := unix.FcntlInt(uintptr(r.fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, ErrAuthoringStorage
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, ErrAuthoringStorage
		}
		fd = next
	}
	fileFD, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	unix.Close(fd)
	if err != nil {
		return nil, ErrAuthoringStorage
	}
	f := os.NewFile(uintptr(fileFD), "authoring-record")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		f.Close()
		return nil, ErrAuthoringStorage
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	ce := f.Close()
	if err != nil || ce != nil || int64(len(data)) > limit {
		return nil, ErrAuthoringStorage
	}
	return data, nil
}

func validBlobName(name string) bool {
	const prefix = "authoring/blobs/"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+64 {
		return false
	}
	for _, c := range strings.TrimPrefix(name, prefix) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
