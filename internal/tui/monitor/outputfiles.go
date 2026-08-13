package monitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jig/internal/datastore"
)

type fileKind int

const (
	kindOther fileKind = iota
	kindMarkdown
	kindJSON
)

var errIsDir = errors.New("output path is a directory")

type outputFile struct {
	name string
	path string
	kind fileKind
	err  error
}

type Option func(*outputFile)

func WithError(error error) Option {
	return func(o *outputFile) { o.err = error }
}

func detectFileKind(path string) fileKind {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return kindMarkdown
	case ".json":
		return kindJSON
	default:
		return kindOther
	}
}

func stepOutputFiles(runDir string, stepId string, declaredOutput string) []outputFile {

	if runDir == "" {
		return nil
	}

	var paths []string

	paths = append(paths,
		datastore.OutputJSONPath(runDir, stepId),
		datastore.OutputPath(runDir, stepId),
	)

	if declaredOutput != "" {
		paths = append(paths, declaredOutput)
	}

	return createOutputFiles(paths)
}

func createOutputFiles(paths []string) []outputFile {
	var files []outputFile
	seen := make(map[string]bool)

	for _, path := range paths {
		key := path
		if abs, err := filepath.Abs(path); err == nil {
			key = abs
		}

		if seen[key] {
			continue
		}

		seen[key] = true
		files = append(files, statOutputFile(path))
	}

	return files
}

func statOutputFile(path string) outputFile {
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		return createFile(path, WithError(err))
	case fi.IsDir():
		return createFile(path, WithError(errIsDir))
	default:
		return createFile(path)
	}
}

func createFile(path string, options ...Option) outputFile {
	file := outputFile{
		kind: detectFileKind(path),
		path: path,
		name: filepath.Base(path),
	}

	for _, opt := range options {
		opt(&file)
	}

	return file
}

const (
	outputFileCap  = 256 * 1024 // 256 KiB max readable file size
	binarySniffLen = 8 * 1024   // scan first 8 KiB for NUL bytes
)

// readOutputFile reads the file at path, enforcing a 256 KiB size cap and a
// binary-detection sniff (NUL byte in first 8 KiB). Returns (content,
// placeholder) where placeholder is non-empty when the file cannot be shown
// as text (binary, oversized, or unreadable).
func readOutputFile(path string, kind fileKind) (content, placeholder string) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", "cannot read file: " + err.Error()
	}
	if fi.Size() > outputFileCap {
		return "", fmt.Sprintf("file too large — %d bytes (max 256 KiB)", fi.Size())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "cannot read file: " + err.Error()
	}

	// Binary detection: NUL byte in first 8 KiB.
	sniff := data
	if len(sniff) > binarySniffLen {
		sniff = sniff[:binarySniffLen]
	}
	for _, b := range sniff {
		if b == 0 {
			return "", "binary file — not shown"
		}
	}

	return string(data), ""
}
