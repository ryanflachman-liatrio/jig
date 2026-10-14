package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

const candidateLimit = 32 << 20

// candidateBuffer drains subprocess output while bounding retained bytes.
type candidateBuffer struct {
	strings.Builder
	overflow bool
}

func (b *candidateBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := candidateLimit - b.Len()
	if n > left {
		b.overflow = true
		p = p[:left]
	}
	_, _ = b.Builder.Write(p)
	return n, nil
}

// collectCandidate runs after validation inside the admitted worker deadline.
// Every Git error fails closed; a missing diff cannot authorize integration.
func collectCandidate(ctx context.Context, dir, base string) (string, []string, error) {
	seen := map[string]bool{}
	var diff candidateBuffer
	pairs := []struct{ names, patch []string }{
		{[]string{"diff", "--name-only", "-z", "--cached"}, []string{"diff", "--cached"}},
		{[]string{"diff", "--name-only", "-z"}, []string{"diff"}},
	}
	if base != "" {
		pairs = append(pairs, struct{ names, patch []string }{[]string{"diff", "--name-only", "-z", base, "HEAD"}, []string{"diff", base, "HEAD"}})
	}
	for _, pair := range pairs {
		out, err := gitCmdContext(ctx, dir, pair.names...)
		if err != nil {
			return "", nil, fmt.Errorf("collect candidate paths: %w", err)
		}
		for _, path := range strings.Split(out, "\x00") {
			if path != "" {
				seen[path] = true
			}
		}
		out, err = gitCmdContext(ctx, dir, pair.patch...)
		if err != nil {
			return "", nil, fmt.Errorf("collect candidate diff: %w", err)
		}
		_, _ = diff.Write([]byte(out))
	}
	out, err := gitCmdContext(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", nil, fmt.Errorf("collect candidate untracked paths: %w", err)
	}
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		seen[path] = true
		patch, err := gitCmdContext(ctx, dir, "diff", "--no-index", "--", "/dev/null", path)
		// git diff returns 1 when differences exist; all other failures reject.
		if err != nil && !diffExitOne(err) {
			return "", nil, fmt.Errorf("collect candidate new file: %w", err)
		}
		_, _ = diff.Write([]byte(patch))
	}
	if diff.overflow {
		return "", nil, fmt.Errorf("candidate diff exceeds 32 MiB")
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return diff.String(), paths, ctx.Err()
}

func diffExitOne(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}
