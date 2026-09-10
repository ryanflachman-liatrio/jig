// Package runexport builds disclosure-safe, local-only diagnostic bundles from
// inactive persisted runs. Its public result deliberately contains no source
// values, paths, or identifiers beyond the caller-supplied destination.
package runexport

import (
	"context"
	"errors"
	"fmt"

	"jig/internal/engine"
)

var (
	// ErrUsage identifies a request that cannot safely name an export target.
	ErrUsage = errors.New("run export: invalid request")
	// ErrOperational identifies a safe operational refusal without source detail.
	ErrOperational = errors.New("run export: unavailable")
)

// Options names the only caller-controlled export inputs. Root is the .jig
// persistence directory; Destination is never created by acquisition.
type Options struct {
	Root        string
	RunID       string
	Destination string
	IncludeText bool
}

// Result is intentionally source-text-free so callers cannot accidentally
// expose collected evidence through logs or command output.
type Result struct {
	Destination string
	Complete    bool
}

// Export acquires the scheduler ownership lease before it touches evidence.
// Archive projection and publication are added by later implementation tasks.
func Export(ctx context.Context, options Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}
	request, err := resolveRequest(options)
	if err != nil {
		return Result{}, err
	}
	lease, err := engine.AcquireRunLease(request.runDir)
	if err != nil {
		return Result{}, fmt.Errorf("%w: run has a live scheduler", ErrOperational)
	}
	defer lease.Close()
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: canceled", ErrOperational)
	}
	inventory, err := collectInventory(request.runDir)
	if err != nil {
		return Result{}, err
	}
	if err := inventory.Recheck(request.runDir); err != nil {
		return Result{}, err
	}
	return Result{Destination: options.Destination}, fmt.Errorf("%w: archive construction is unavailable", ErrOperational)
}
