package runner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryanflachman-liatrio/jig/internal/engine"
	"github.com/ryanflachman-liatrio/jig/internal/step"
)

const containerCollectionTimeout = 30 * time.Second

// publishContainerOutput is a collection primitive, not execution authority.
// The attempt finalizer may call it only after engine-acknowledged quiescence
// and all validation gates pass. Runtime finalizer wiring remains separate.
// A directory rename publishes bytes and metadata together. Only the returned
// path may be selected by a successful result; failed publication never returns
// a usable artifact reference, including when directory sync fails after rename.
func publishContainerOutput(ctx context.Context, req engine.StepRequest, w *containerWorkspace) (string, step.FileArtifact, error) {
	return collectContainerOutput(ctx, req, w, nil)
}

func collectContainerOutput(ctx context.Context, req engine.StepRequest, w *containerWorkspace, check func(string) error) (output string, metadata step.FileArtifact, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, containerCollectionTimeout)
	defer cancel()
	defer func() {
		if resultErr != nil {
			output = ""
			metadata = step.FileArtifact{}
			if ctx.Err() != nil {
				resultErr = ctx.Err()
			} else {
				resultErr = fmt.Errorf("container output publication failed")
			}
		}
	}()
	checkpoint := func(stage string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			return check(stage)
		}
		return nil
	}
	if req.Step == nil || !validContainerOutputPath(req.Step.Output) {
		return "", metadata, fmt.Errorf("invalid declared output")
	}
	nonce, err := hex.DecodeString(req.ExecutionNonce)
	if err != nil || len(nonce) != 16 || req.ExecutionNonce != strings.ToLower(req.ExecutionNonce) || !filepath.IsAbs(req.ArtifactDir) {
		return "", metadata, fmt.Errorf("missing publication ownership")
	}
	if err := checkpoint("open"); err != nil {
		return "", metadata, err
	}
	source, info, err := openContainerOutput(w, req.Step.Output)
	if err != nil {
		return "", metadata, err
	}
	defer source.Close()
	if info.Size() > step.ContainerInputFileBytes {
		return "", metadata, fmt.Errorf("output byte limit exceeded")
	}
	// ArtifactDir is engine-owned and must already exist. Collection never
	// creates a relative directory or infers persistence-off ownership.
	store, err := os.OpenRoot(req.ArtifactDir)
	if err != nil {
		return "", metadata, err
	}
	defer store.Close()
	final := "container-" + req.ExecutionNonce
	if _, err := store.Lstat(final); !os.IsNotExist(err) {
		return "", metadata, fmt.Errorf("artifact identity already exists")
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", metadata, err
	}
	pending := ".pending-" + hex.EncodeToString(suffix[:])
	if err := store.Mkdir(pending, 0700); err != nil {
		return "", metadata, err
	}
	defer func() {
		if err := store.RemoveAll(pending); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	target, err := store.OpenFile(filepath.Join(pending, "output"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", metadata, err
	}
	h := sha256.New()
	writer := io.MultiWriter(target, h)
	reader := io.LimitReader(source, info.Size()+1)
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if err = checkpoint("copy"); err != nil {
			break
		}
		var n int
		n, err = reader.Read(buffer)
		if total+int64(n) > info.Size() {
			err = fmt.Errorf("output grew")
			break
		}
		if n > 0 {
			var writeErr error
			_, writeErr = writer.Write(buffer[:n])
			total += int64(n)
			if writeErr != nil {
				err = writeErr
				break
			}
		}
		if err == io.EOF {
			err = nil
			break
		}
		if err != nil {
			break
		}
	}
	if err == nil && total != info.Size() {
		err = fmt.Errorf("output size changed")
	}
	if err == nil {
		after, statErr := source.Stat()
		if statErr != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || !singleLinkFile(after) {
			err = fmt.Errorf("output changed during collection")
		}
	}
	mode := uint32(0444)
	if info.Mode()&0111 != 0 {
		mode = 0555
	}
	if err == nil {
		err = target.Chmod(os.FileMode(mode))
	}
	if err == nil {
		err = checkpoint("file_sync")
	}
	if err == nil {
		err = target.Sync()
	}
	err = errors.Join(err, target.Close(), source.Close())
	if err != nil {
		return "", metadata, err
	}
	metadata = step.FileArtifact{SHA256: hex.EncodeToString(h.Sum(nil)), Size: total, Mode: mode}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", metadata, err
	}
	if err := checkpoint("metadata"); err != nil {
		return "", metadata, err
	}
	record, err := store.OpenFile(filepath.Join(pending, "metadata.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		return "", metadata, err
	}
	_, err = record.Write(raw)
	if err == nil {
		err = record.Sync()
	}
	err = errors.Join(err, record.Close())
	if err != nil {
		return "", metadata, err
	}
	syncDir := func(name string) error {
		dir, e := store.Open(name)
		if e != nil {
			return e
		}
		e = dir.Sync()
		return errors.Join(e, dir.Close())
	}
	if err := syncDir(pending); err != nil {
		return "", metadata, err
	}
	if err := checkpoint("rename"); err != nil {
		return "", metadata, err
	}
	if err := store.Rename(pending, final); err != nil {
		return "", metadata, err
	}
	// After rename a failure retains an unaccepted, execution-specific artifact.
	// Never remove or overwrite it on retry: a fresh nonce is mandatory.
	if err := checkpoint("directory_sync"); err != nil {
		return "", metadata, err
	}
	if err := syncDir("."); err != nil {
		return "", metadata, err
	}
	if err := ctx.Err(); err != nil {
		return "", metadata, err
	}
	return filepath.Join(req.ArtifactDir, final, "output"), metadata, nil
}

func validContainerOutputPath(name string) bool {
	return name != "" && len(name) <= 4096 && filepath.IsLocal(name) && filepath.Clean(name) == name && !strings.ContainsAny(name, "\\\x00") && !deniedContainerSource(name)
}

func openContainerOutput(w *containerWorkspace, name string) (*os.File, os.FileInfo, error) {
	if !validContainerOutputPath(name) {
		return nil, nil, fmt.Errorf("invalid output path")
	}
	if w == nil || !filepath.IsAbs(w.Root) {
		return nil, nil, fmt.Errorf("missing workspace ownership")
	}
	root, err := os.OpenRoot(w.Root)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || w.identity == nil || !os.SameFile(info, w.identity) {
		return nil, nil, fmt.Errorf("staging ownership changed")
	}
	// Reject symlinks in every component, even contained links. The stopped
	// workspace has no writer; Root confinement also protects against escapes.
	relative := filepath.Join("workspace", name)
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for i := range parts {
		info, err = root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("output path unavailable or symlink")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, nil, fmt.Errorf("output parent not directory")
		}
	}
	file, err := openContainerInput(root, relative)
	if err != nil {
		return nil, nil, err
	}
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) || !singleLinkFile(actual) {
		file.Close()
		return nil, nil, fmt.Errorf("output is not an owned regular file")
	}
	return file, actual, nil
}
