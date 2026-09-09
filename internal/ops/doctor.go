package ops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/headless"
	"jig/internal/workflow"
)

type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

type Check struct {
	ID          string      `json:"id"`
	Status      CheckStatus `json:"status"`
	Scope       string      `json:"scope"`
	Message     string      `json:"message"`
	Remediation string      `json:"remediation,omitempty"`
}

type DoctorReport struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

type DoctorOptions struct {
	Root         string
	WorkflowPath string
	Workflow     *workflow.Workflow
	CI           bool
	Cwd          string
	LookPath     func(string) (string, error)
	LookupEnv    func(string) (string, bool)
	RunGit       func(string, ...string) error
}

func Doctor(opts DoctorOptions) DoctorReport {
	if opts.Cwd == "" {
		opts.Cwd = "."
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.RunGit == nil {
		opts.RunGit = func(dir string, args ...string) error {
			cmdArgs := append([]string{"-C", dir}, args...)
			cmd := exec.Command("git", cmdArgs...)
			cmd.Stdout, cmd.Stderr = nil, nil
			return cmd.Run()
		}
	}

	checks := []Check{checkStoreAccess(opts.Root), checkStoredRuns(opts.Root)}
	wf := opts.Workflow
	if wf == nil && opts.WorkflowPath != "" {
		var err error
		wf, err = workflow.Load(opts.WorkflowPath)
		if err != nil {
			checks = append([]Check{{ID: "workflow.load", Status: CheckFail, Scope: opts.WorkflowPath, Message: err.Error(), Remediation: "fix the workflow validation errors"}}, checks...)
			return finishDoctor(checks)
		}
	}
	if wf != nil {
		checks = append([]Check{{ID: "workflow.load", Status: CheckPass, Scope: workflowScope(opts), Message: "workflow loaded and validated"}}, checks...)
	}

	if wf == nil {
		checks = append(checks, binaryCheck("git.binary", "git", "optional", false, opts.LookPath, "install git to use worktree-isolated workflows"))
		checks = append(checks, optionalBackendChecks(opts.LookPath)...)
		return finishDoctor(checks)
	}

	needsWorktree := false
	backends := map[string]bool{}
	requiredTools := map[string]bool{}
	secrets := map[string]bool{}
	for i := range wf.Steps {
		s := &wf.Steps[i]
		if s.Isolation == workflow.IsolationWorktree {
			needsWorktree = true
		}
		if s.Type == workflow.StepAgent {
			backends[s.Backend+"\x00"+s.Transport] = true
		}
		if s.Findings != nil {
			for _, tool := range s.Findings.RequiredTools {
				requiredTools[tool] = true
			}
		}
		for _, name := range s.Secrets {
			secrets[name] = true
		}
	}
	if needsWorktree {
		checks = append(checks, binaryCheck("git.binary", "git", "workflow", true, opts.LookPath, "install git and ensure it is on PATH"))
		if _, err := opts.LookPath("git"); err != nil {
			checks = append(checks, Check{ID: "git.repository", Status: CheckFail, Scope: opts.Cwd, Message: "repository readiness cannot be checked without git", Remediation: "install git and ensure it is on PATH"})
		} else {
			if err := opts.RunGit(opts.Cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
				checks = append(checks, Check{ID: "git.repository", Status: CheckFail, Scope: opts.Cwd, Message: "worktree isolation requires a git working tree", Remediation: "run from a git repository with a valid HEAD"})
			} else if err := opts.RunGit(opts.Cwd, "rev-parse", "--verify", "HEAD"); err != nil {
				checks = append(checks, Check{ID: "git.repository", Status: CheckFail, Scope: opts.Cwd, Message: "git repository has no HEAD", Remediation: "create an initial commit"})
			} else {
				checks = append(checks, Check{ID: "git.repository", Status: CheckPass, Scope: opts.Cwd, Message: "git work tree and HEAD are available"})
			}
		}
	}
	keys := sortedKeys(backends)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		checks = append(checks, backendChecks(parts[0], parts[1], true, opts.LookPath)...)
	}
	for _, tool := range sortedKeys(requiredTools) {
		checks = append(checks, binaryCheck("check.required_tool", tool, tool, true, opts.LookPath, fmt.Sprintf("install %s and ensure it is on PATH", tool)))
	}
	for _, name := range sortedKeys(secrets) {
		key := secretKey(name)
		if _, ok := opts.LookupEnv(key); ok {
			checks = append(checks, Check{ID: "secret.environment", Status: CheckPass, Scope: name, Message: key + " is set"})
		} else {
			checks = append(checks, Check{ID: "secret.environment", Status: CheckFail, Scope: name, Message: key + " is not set", Remediation: "set " + key + " in the command environment"})
		}
	}
	if opts.CI {
		gates := headless.InventoryGates(wf)
		if len(gates) == 0 {
			checks = append(checks, Check{ID: "ci.human_gate", Status: CheckPass, Scope: "workflow", Message: "no unattended-hostile gates found"})
		} else {
			for _, gate := range gates {
				checks = append(checks, Check{ID: "ci.human_gate", Status: CheckFail, Scope: gate.StepID, Message: gate.Detail, Remediation: "use the TUI or remove the human gate"})
			}
		}
		checks = append(checks, Check{ID: "ci.final_merge", Status: CheckPass, Scope: "workflow", Message: "CI mode discards final merge requests"})
	}
	return finishDoctor(checks)
}

func workflowScope(opts DoctorOptions) string {
	if opts.WorkflowPath != "" {
		return opts.WorkflowPath
	}
	return "workflow"
}

func checkStoreAccess(root string) Check {
	if root == "" {
		return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: "persistence root is empty", Remediation: "pass --root PATH"}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: err.Error()}
	}
	probeDir := abs
	for {
		info, statErr := os.Stat(probeDir)
		if statErr == nil {
			if !info.IsDir() {
				return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: "nearest existing path is not a directory"}
			}
			break
		}
		parent := filepath.Dir(probeDir)
		if parent == probeDir {
			return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: statErr.Error()}
		}
		probeDir = parent
	}
	f, err := os.CreateTemp(probeDir, ".jig-doctor-*")
	if err != nil {
		return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: "persistence root is not writable", Remediation: "choose a readable and writable --root"}
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		return Check{ID: "store.access", Status: CheckFail, Scope: root, Message: "store write probe could not be cleaned up", Remediation: "check filesystem permissions"}
	}
	return Check{ID: "store.access", Status: CheckPass, Scope: root, Message: "persistence location is readable and writable"}
}

func checkStoredRuns(root string) Check {
	ids, err := datastore.ListRunIDs(root)
	if err != nil {
		return Check{ID: "store.runs", Status: CheckFail, Scope: root, Message: err.Error(), Remediation: "repair permissions under the runs directory"}
	}
	var unhealthy []string
	for _, id := range ids {
		runDir, resolveErr := datastore.ResolveRunDir(root, id)
		if resolveErr != nil {
			unhealthy = append(unhealthy, id)
			continue
		}
		records, replayErr := engine.ReplayJournalRecords(runDir)
		if replayErr != nil || len(records) == 0 {
			unhealthy = append(unhealthy, id)
		}
	}
	if len(unhealthy) > 0 {
		return Check{ID: "store.runs", Status: CheckWarn, Scope: root, Message: "unhealthy run directories: " + strings.Join(unhealthy, ", "), Remediation: "inspect with jig status RUN_ID"}
	}
	return Check{ID: "store.runs", Status: CheckPass, Scope: root, Message: fmt.Sprintf("%d persisted run(s) readable", len(ids))}
}

func optionalBackendChecks(lookPath func(string) (string, error)) []Check {
	var checks []Check
	for _, pair := range [][2]string{{"claude", "sdk"}, {"claude", "acp"}, {"cursor", "acp"}, {"codex", "acp"}} {
		checks = append(checks, backendChecks(pair[0], pair[1], false, lookPath)...)
	}
	return checks
}

func backendChecks(backend, transport string, required bool, lookPath func(string) (string, error)) []Check {
	id := "backend." + backend + "." + transport
	tools := []string{}
	login := ""
	switch backend + "/" + transport {
	case "claude/sdk":
		tools, login = []string{"claude"}, "claude"
	case "claude/acp":
		tools, login = []string{"npx", "claude"}, "claude"
	case "cursor/acp":
		tools, login = []string{"cursor-agent"}, "cursor-agent login"
	case "codex/acp":
		tools, login = []string{"npx", "codex"}, "codex login"
	default:
		return []Check{{ID: id, Status: CheckFail, Scope: backend + "/" + transport, Message: "unsupported backend/transport"}}
	}
	missing := []string{}
	for _, tool := range tools {
		if _, err := lookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		status := CheckWarn
		if required {
			status = CheckFail
		}
		return []Check{{ID: id, Status: status, Scope: backend + "/" + transport, Message: "missing executable(s): " + strings.Join(missing, ", "), Remediation: "install prerequisites and ensure they are on PATH"}}
	}
	message := "local executables available; login validity unverified (run `" + login + "`)"
	if transport == "acp" && (backend == "claude" || backend == "codex") {
		message += "; pinned npx adapter may require network or a populated cache"
	}
	return []Check{{ID: id, Status: CheckWarn, Scope: backend + "/" + transport, Message: message}}
}

func binaryCheck(id, binary, scope string, required bool, lookPath func(string) (string, error), remediation string) Check {
	if _, err := lookPath(binary); err != nil {
		status := CheckWarn
		if required {
			status = CheckFail
		}
		return Check{ID: id, Status: status, Scope: scope, Message: binary + " is not on PATH", Remediation: remediation}
	}
	return Check{ID: id, Status: CheckPass, Scope: scope, Message: binary + " is on PATH"}
}

func secretKey(name string) string {
	return "JIG_SECRET_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func finishDoctor(checks []Check) DoctorReport {
	report := DoctorReport{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == CheckFail {
			report.OK = false
			break
		}
	}
	return report
}
