package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"jig/internal/datastore"
	"jig/internal/engine"
	"jig/internal/headless"
	"jig/internal/operatorcfg"
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
		checks = append(checks, optionalBackendChecks(opts.LookPath, opts.Cwd)...)
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
			backends[s.Backend] = true
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
	for _, backend := range sortedKeys(backends) {
		checks = append(checks, backendChecks(backend, true, opts.LookPath, opts.Cwd)...)
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

func optionalBackendChecks(lookPath func(string) (string, error), cwd string) []Check {
	var checks []Check
	for _, backend := range []string{"claude", "cursor", "codex"} {
		checks = append(checks, backendChecks(backend, false, lookPath, cwd)...)
	}
	return checks
}

func backendChecks(backend string, required bool, lookPath func(string) (string, error), cwd string) []Check {
	checks, installed := backendExecutableChecks(backend, required, lookPath)
	if !installed {
		return checks
	}
	switch backend {
	case "claude":
		checks = append(checks, claudeUserSettingsChecks()...)
	case "cursor", "codex":
		checks = append(checks, operatorAllowlistChecks(backend, cwd)...)
	}
	return checks
}

// operatorAllowlistChecks lists the operator-local Codex or Cursor config that
// auto-approves calls without a prompt, so the Tier-1 guard never sees them.
// Blanket approval fails guarded steps at run time, so it is a failure here;
// narrow entries are warnings. Entries are named by file and index only.
func operatorAllowlistChecks(backend, cwd string) []Check {
	id := "backend." + backend + ".operator_allowlist"
	root := cwd
	if abs, err := filepath.Abs(cwd); err == nil {
		root = abs
	}
	findings, err := operatorcfg.Inspect(backend, root, root, os.Getenv)
	if err != nil {
		return []Check{{ID: id, Status: CheckFail, Scope: backend, Message: err.Error(), Remediation: "fix the config file; guarded steps fail until it can be read"}}
	}
	var checks []Check
	for _, f := range findings {
		if f.Class == operatorcfg.Blanket {
			checks = append(checks, Check{ID: id, Status: CheckFail, Scope: f.File, Message: f.String() + " auto-approves every tool call, so guarded steps on this backend fail before they start", Remediation: "remove the setting, or set [step.security] tier1_enabled = false on the affected steps"})
			continue
		}
		checks = append(checks, Check{ID: id, Status: CheckWarn, Scope: f.File, Message: f.String() + " auto-approves matching tool calls without a prompt, so the Tier-1 guard does not see them"})
	}
	return checks
}

// claudeUserSettingsChecks warns when ~/.claude/settings.json sets keys an
// operator may rely on for authentication. jig runs Claude with
// settingSources: ["project"], so user-level settings do not apply. The
// warning names the keys only, never their values or env variable names.
func claudeUserSettingsChecks() []Check {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		return []Check{{ID: "backend.claude.user_settings", Status: CheckWarn, Scope: path, Message: "user settings could not be parsed, so jig cannot check them for env or apiKeyHelper"}}
	}
	var keys []string
	for _, key := range []string{"env", "apiKeyHelper"} {
		if _, ok := settings[key]; ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return []Check{{
		ID:          "backend.claude.user_settings",
		Status:      CheckWarn,
		Scope:       path,
		Message:     "user settings set " + strings.Join(keys, " and ") + ", but jig runs Claude with settingSources: [\"project\"], so user-level auth and env settings do not apply",
		Remediation: "provide Claude credentials through your login or the process environment rather than user settings",
	}}
}

// backendExecutableChecks reports whether the backend's executables are on
// PATH; installed is false for a missing or unsupported backend.
func backendExecutableChecks(backend string, required bool, lookPath func(string) (string, error)) (checks []Check, installed bool) {
	id := "backend." + backend
	tools := []string{}
	login := ""
	switch backend {
	case "claude":
		tools, login = []string{"npx", "claude"}, "claude"
	case "cursor":
		tools, login = []string{"cursor-agent"}, "cursor-agent login"
	case "codex":
		tools, login = []string{"npx", "codex"}, "codex login"
	default:
		return []Check{{ID: id, Status: CheckFail, Scope: backend, Message: "unsupported backend"}}, false
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
		return []Check{{ID: id, Status: status, Scope: backend, Message: "missing executable(s): " + strings.Join(missing, ", "), Remediation: "install prerequisites and ensure they are on PATH"}}, false
	}
	message := "local executables available; login validity unverified (run `" + login + "`)"
	if backend == "claude" || backend == "codex" {
		message += "; pinned npx adapter may require network or a populated cache"
	}
	return []Check{{ID: id, Status: CheckWarn, Scope: backend, Message: message}}, true
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
