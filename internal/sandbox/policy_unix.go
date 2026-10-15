//go:build darwin || linux

package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Pin each canonical ancestor; allow root-owned sticky system temporary
// directories but never writable policy files or untrusted writable parents.
func readPolicy(path string) ([]byte, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		_ = unix.Close(fd)
		fd = next
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || !trustedOwner(stat.Uid) || stat.Mode&0022 != 0 && !(stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0) {
			return nil, fmt.Errorf("unsafe policy ancestor")
		}
	}
	fileFD, err := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fileFD), "sandbox-policy")
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fileFD, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || !trustedOwner(stat.Uid) || stat.Mode&0022 != 0 || stat.Size > policyLimit {
		return nil, fmt.Errorf("unsafe policy file")
	}
	data, err := io.ReadAll(io.LimitReader(f, policyLimit+1))
	if err != nil || len(data) > policyLimit {
		return nil, fmt.Errorf("policy read failed")
	}
	return data, nil
}

func trustedOwner(uid uint32) bool { return uid == 0 || uid == uint32(os.Geteuid()) }

// Docker metadata permits canonical file links, but never devices or pipes.
func readMetadata(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "docker-metadata")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > policyLimit {
		return nil, fmt.Errorf("invalid metadata file")
	}
	data, err := io.ReadAll(io.LimitReader(f, policyLimit+1))
	if err != nil || len(data) > policyLimit {
		return nil, fmt.Errorf("metadata limit")
	}
	return data, nil
}
