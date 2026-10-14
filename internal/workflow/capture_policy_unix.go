//go:build darwin || linux

package workflow

import (
	"bytes"
	"crypto/sha256"
	"golang.org/x/sys/unix"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const captureFileLimit = 1 << 20

type captureError = CaptureError

const (
	captureLimit       = CaptureLimit
	captureForbidden   = CaptureForbidden
	captureUnsupported = CaptureUnsupported
	captureIntegrity   = CaptureIntegrity
	captureStorage     = CaptureStorage
)

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

func captureValidatePath(path string) error {
	if !utf8.ValidString(path) || len(path) > 4096 {
		return captureLimit
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return captureForbidden
		}
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, part := range parts {
		if len(part) > 255 {
			return captureLimit
		}
		folded := cases.Fold().String(part)
		switch folded {
		case ".git", ".jig", ".ssh", ".aws", ".azure", ".kube", ".gnupg", ".docker", ".env", ".netrc", ".npmrc", ".pypirc", "auth.json", "credentials.json":
			return captureForbidden
		}
		if strings.HasPrefix(folded, ".env.") {
			return captureForbidden
		}
		if folded == "gcloud" && i > 0 && cases.Fold().String(parts[i-1]) == ".config" {
			return captureForbidden
		}
	}
	clean := filepath.Clean(path)
	for _, root := range []string{"/dev", "/proc", "/sys"} {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return captureForbidden
		}
	}
	return nil
}

// Canonicalize only the selected anchor, then pin each component without
// following links. Files below this anchor never use a path-based ordinary open.
func capturePin(path string) (*os.File, error) {
	if err := captureValidatePath(path); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, captureStorage
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, captureStorage
	}
	if err := captureValidatePath(canonical); err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, captureStorage
	}
	for _, component := range strings.Split(strings.TrimPrefix(canonical, "/"), "/") {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, captureStorage
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), canonical), nil
}

func (l *captureLedger) capture(root *os.File, relative string, afterOpen func()) (captureCapture, error) {
	var empty captureCapture
	if err := captureValidatePath(relative); err != nil {
		return empty, err
	}
	if filepath.IsAbs(relative) {
		return empty, captureForbidden
	}
	if l.bindings >= 2048 {
		return empty, captureLimit
	}
	l.bindings++
	localLinks := 0
	for {
		clean := filepath.Clean(relative)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return empty, captureForbidden
		}
		parts := strings.Split(clean, "/")
		if len(parts) > 64 {
			return empty, captureLimit
		}
		fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return empty, captureStorage
		}
		for _, part := range parts[:len(parts)-1] {
			next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			unix.Close(fd)
			if openErr != nil {
				return empty, captureForbidden
			}
			fd = next
		}
		name := parts[len(parts)-1]
		var before unix.Stat_t
		err = unix.Fstatat(fd, name, &before, unix.AT_SYMLINK_NOFOLLOW)
		if err != nil {
			unix.Close(fd)
			return empty, captureStorage
		}
		if before.Mode&unix.S_IFMT == unix.S_IFLNK {
			if localLinks >= 16 || l.links >= 1024 {
				unix.Close(fd)
				return empty, captureLimit
			}
			localLinks++
			l.links++
			buf := make([]byte, 4097)
			n, readErr := unix.Readlinkat(fd, name, buf)
			unix.Close(fd)
			if readErr != nil {
				return empty, captureStorage
			}
			if n > 4096 {
				return empty, captureLimit
			}
			target := string(buf[:n])
			if filepath.IsAbs(target) {
				return empty, captureForbidden
			}
			if err := captureValidatePath(target); err != nil {
				return empty, err
			}
			relative = filepath.Join(filepath.Dir(clean), target)
			if err := captureValidatePath(relative); err != nil {
				return empty, err
			}
			continue
		}
		fileFD, openErr := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if openErr != nil {
			unix.Close(fd)
			return empty, captureUnsupported
		}
		f := os.NewFile(uintptr(fileFD), "p3-source")
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			f.Close()
			unix.Close(fd)
			return empty, captureUnsupported
		}
		var opened unix.Stat_t
		if unix.Fstat(fileFD, &opened) != nil || opened.Dev != before.Dev || opened.Ino != before.Ino {
			f.Close()
			unix.Close(fd)
			return empty, captureIntegrity
		}
		if afterOpen != nil {
			afterOpen()
		}
		allowance := min(captureFileLimit, (32<<20)-l.read)
		if allowance < 0 {
			allowance = 0
		}
		data, readErr := io.ReadAll(io.LimitReader(f, int64(allowance)+1))
		l.read += len(data)
		var after, pathAfter unix.Stat_t
		statErr = unix.Fstat(fileFD, &after)
		pathErr := unix.Fstatat(fd, name, &pathAfter, unix.AT_SYMLINK_NOFOLLOW)
		closeErr := f.Close()
		unix.Close(fd)
		if readErr != nil || closeErr != nil || statErr != nil {
			return empty, captureStorage
		}
		if len(data) > allowance {
			return empty, captureLimit
		}
		if pathErr != nil || pathAfter.Dev != opened.Dev || pathAfter.Ino != opened.Ino || pathAfter.Mode&unix.S_IFMT != unix.S_IFREG || !captureSameSource(opened, after) {
			return empty, captureIntegrity
		}
		digest := sha256.Sum256(data)
		if l.blobs == nil {
			l.blobs = make(map[[32]byte][]byte)
		}
		if _, ok := l.blobs[digest]; !ok {
			if len(l.blobs) >= 1024 || l.unique+len(data) > 16<<20 {
				return empty, captureLimit
			}
			l.blobs[digest] = bytes.Clone(data)
			l.unique += len(data)
		}
		return captureCapture{bytes: data, digest: digest, executable: info.Mode()&0111 != 0, source: filepath.Join(root.Name(), clean)}, nil
	}
}

// Reads may update atime; changes to source identity, size, permissions,
// link count, mtime or ctime remain observable integrity failures.
func captureSameSource(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Size == b.Size && a.Mode == b.Mode &&
		a.Nlink == b.Nlink && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

// captureNamespace rejects collisions before any content is read. Directories and
// files share one namespace; an implicit file prefix cannot become a directory.
type captureNamespace map[string]bool

func (n captureNamespace) add(path string, directory bool) error {
	if !norm.NFC.IsNormalString(path) {
		return captureForbidden
	}
	key := cases.Fold().String(path)
	if _, exists := n[key]; exists {
		return captureForbidden
	}
	for prefix := filepath.Dir(key); prefix != "."; prefix = filepath.Dir(prefix) {
		if isDir, exists := n[prefix]; exists && !isDir {
			return captureForbidden
		}
	}
	if !directory {
		for old := range n {
			if strings.HasPrefix(old, key+"/") {
				return captureForbidden
			}
		}
	}
	n[key] = directory
	return nil
}

func captureSelectBundle(path, project string) (*os.File, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, captureStorage
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, captureStorage
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, captureStorage
	}
	for _, protected := range []string{home, project} {
		if protected == "" {
			continue
		}
		protected, err = filepath.EvalSymlinks(protected)
		if err != nil {
			return nil, captureStorage
		}
		protected, err = filepath.Abs(protected)
		if err != nil {
			return nil, captureStorage
		}
		if canonical == protected || withinDir(canonical, protected) {
			return nil, captureForbidden
		}
	}
	return capturePin(path)
}

func (l *captureLedger) bundle(root *os.File) (map[string]captureCapture, error) {
	files := make(map[string]captureCapture)
	names := make(captureNamespace)
	total := 0
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, prefix string, depth int) error {
		if depth > 16 {
			return captureLimit
		}
		for {
			entries, err := dir.ReadDir(64)
			if err != nil && err != io.EOF {
				return captureStorage
			}
			for _, entry := range entries {
				if l.visited >= 8192 {
					return captureLimit
				}
				l.visited++
				name := filepath.Join(prefix, entry.Name())
				if err := captureValidatePath(name); err != nil {
					return err
				}
				if err := names.add(name, entry.IsDir()); err != nil {
					return err
				}
				if entry.IsDir() {
					fd, err := unix.Openat(int(dir.Fd()), entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if err != nil {
						return captureForbidden
					}
					child := os.NewFile(uintptr(fd), "p3-directory")
					walkErr := walk(child, name, depth+1)
					closeErr := child.Close()
					if walkErr != nil {
						return walkErr
					}
					if closeErr != nil {
						return captureStorage
					}
				} else {
					if len(files) >= 256 {
						return captureLimit
					}
					captured, err := l.capture(root, name, nil)
					if err != nil {
						return err
					}
					if total+len(captured.bytes) > 4<<20 {
						return captureLimit
					}
					total += len(captured.bytes)
					files[name] = captured
				}
			}
			if err == io.EOF {
				return nil
			}
		}
	}
	// Reopening the descriptor avoids consuming the caller's directory offset.
	fd, err := unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, captureStorage
	}
	dir := os.NewFile(uintptr(fd), "p3-bundle")
	err = walk(dir, "", 0)
	closeErr := dir.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, captureStorage
	}
	return files, nil
}
