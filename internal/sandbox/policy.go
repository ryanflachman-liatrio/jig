// Package sandbox owns operator policy and deployment inspection. It does not
// execute workloads or own scheduler state.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/distribution/reference"

	"github.com/ryanflachman-liatrio/jig/internal/workflow"
)

const policyLimit = 1 << 20

// Source is a selector and audit provenance, never an authorization token.
type Source struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

// Selection captures process-entry inputs independently of project preferences.
type Selection struct {
	ExplicitPath  string
	Cwd           string
	Project       string
	Home          string
	XDGConfigHome string
}

// Failure contains only bounded identifiers, never parser errors or input data.
type Failure struct {
	Reason string `json:"reason"`
	Field  string `json:"field"`
	StepID string `json:"step_id,omitempty"`
	Source Source `json:"source"`
}

func (e *Failure) Error() string {
	return fmt.Sprintf("%s: field=%s step=%s source=%s", e.Reason, Display(e.Field), Display(e.StepID), Display(e.Source.Kind))
}

// Display escapes controls and limits the resulting UTF-8 representation.
func Display(s string) string {
	var b strings.Builder
	for _, r := range s {
		v := string(r)
		if unicode.IsControl(r) {
			v = fmt.Sprintf("\\u%04x", r)
		}
		if b.Len()+len(v) > 509 {
			b.WriteString("...")
			break
		}
		b.WriteString(v)
	}
	return b.String()
}

type limits struct {
	CPUs   *workflow.SandboxCPU    `toml:"cpus"`
	Memory *workflow.SandboxMemory `toml:"memory"`
	PIDs   *int64                  `toml:"pids"`
}

type dockerPolicy struct {
	Endpoints      []string `toml:"allowed_endpoints"`
	Repositories   []string `toml:"allowed_repositories"`
	Images         []string `toml:"allowed_images"`
	Digest         bool     `toml:"require_digest"`
	Networks       []string `toml:"allowed_networks"`
	Emulation      bool     `toml:"allow_emulation"`
	WorkspaceRoots []string `toml:"workspace_roots"`
	StorageRoots   []string `toml:"storage_roots"`
	Authentication string   `toml:"authentication"`
	Defaults       limits   `toml:"defaults"`
	Ceilings       limits   `toml:"ceilings"`
}

type document struct {
	Version          int           `toml:"version"`
	AllowHost        *bool         `toml:"allow_host"`
	RequireAllAgents bool          `toml:"require_all_agents"`
	HardStorageBytes *int64        `toml:"hard_storage_bytes"`
	Docker           *dockerPolicy `toml:"docker"`
}

// Policy has no mutable exported fields. One value is frozen for a run epoch.
type Policy struct {
	doc         document
	source      Source
	fingerprint string
}

func (p *Policy) Source() Source        { return p.source }
func (p *Policy) Fingerprint() string   { return p.fingerprint }
func (p *Policy) HasDocker() bool       { return p != nil && p.doc.Docker != nil }
func (p *Policy) AllowsEmulation() bool { return p.HasDocker() && p.doc.Docker.Emulation }
func (p *Policy) Endpoints() []string {
	if !p.HasDocker() {
		return nil
	}
	return slices.Clone(p.doc.Docker.Endpoints)
}

func (p *Policy) failure(reason, field, step string) error {
	source := Source{Kind: "builtin"}
	if p != nil {
		source = p.source
	}
	return &Failure{Reason: reason, Field: field, StepID: Display(step), Source: source}
}

// Load never consults workflow/config preferences, Docker stores, or secrets.
func Load(in Selection) (*Policy, error) {
	p := &Policy{source: Source{Kind: "implicit"}}
	if !filepath.IsAbs(in.Cwd) {
		return nil, p.failure("sandbox_policy_invalid", "cwd", "")
	}
	path := in.ExplicitPath
	if path != "" {
		p.source.Kind = "explicit"
		if !filepath.IsAbs(path) {
			path = filepath.Join(in.Cwd, path)
		}
	} else {
		root := in.XDGConfigHome
		if root == "" {
			if !filepath.IsAbs(in.Home) {
				return nil, p.failure("sandbox_policy_invalid", "home", "")
			}
			root = filepath.Join(in.Home, ".config")
		}
		if !filepath.IsAbs(root) {
			return nil, p.failure("sandbox_policy_invalid", "xdg_config_home", "")
		}
		path = filepath.Join(root, "jig", "sandbox-policy.toml")
	}
	p.source.Path = filepath.Clean(path)
	parent, err := canonicalPath(filepath.Dir(path))
	if err != nil {
		return nil, p.failure("sandbox_policy_unsafe", "source", "")
	}
	if p.source.Kind == "implicit" {
		project := in.Project
		if project == "" {
			project = in.Cwd
		}
		project, err = canonicalPath(project)
		if err != nil || beneath(project, filepath.Join(parent, filepath.Base(path))) {
			return nil, p.failure("sandbox_policy_unsafe", "source", "")
		}
	}
	data, err := readPolicy(filepath.Join(parent, filepath.Base(path)))
	if os.IsNotExist(err) && p.source.Kind == "implicit" {
		allow := true
		p.doc = document{Version: 1, AllowHost: &allow}
		p.source = Source{Kind: "builtin"}
		p.seal()
		return p, nil
	}
	if err != nil {
		return nil, p.failure("sandbox_policy_unavailable", "source", "")
	}
	md, err := toml.Decode(string(data), &p.doc)
	if err != nil || len(md.Undecoded()) != 0 {
		return nil, p.failure("sandbox_policy_invalid", "document", "")
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	p.source.Path = filepath.Join(parent, filepath.Base(path))
	p.seal()
	return p, nil
}

func (p *Policy) seal() {
	raw, _ := json.Marshal(p.doc)
	sum := sha256.Sum256(raw)
	p.fingerprint = hex.EncodeToString(sum[:])
}

func (p *Policy) validate() error {
	bad := func(field string) error { return p.failure("sandbox_policy_invalid", field, "") }
	if p.doc.Version != 1 {
		return bad("version")
	}
	if p.doc.AllowHost == nil {
		return bad("allow_host")
	}
	if p.doc.HardStorageBytes != nil && *p.doc.HardStorageBytes <= 0 {
		return bad("hard_storage_bytes")
	}
	d := p.doc.Docker
	if d == nil {
		return nil
	}
	lists := []struct {
		name   string
		values *[]string
	}{
		{"allowed_endpoints", &d.Endpoints}, {"allowed_repositories", &d.Repositories}, {"allowed_images", &d.Images},
		{"allowed_networks", &d.Networks}, {"workspace_roots", &d.WorkspaceRoots}, {"storage_roots", &d.StorageRoots},
	}
	for _, list := range lists {
		if *list.values == nil || len(*list.values) > 256 {
			return bad("docker." + list.name)
		}
		seen := map[string]bool{}
		for i, v := range *list.values {
			if v == "" || strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return bad("docker." + list.name)
			}
			switch list.name {
			case "allowed_images", "allowed_repositories":
				ref, err := reference.ParseNormalizedNamed(v)
				if err != nil {
					return bad("docker." + list.name)
				}
				_, tagged := ref.(reference.Tagged)
				_, digested := ref.(reference.Digested)
				if tagged && digested || list.name == "allowed_repositories" && (tagged || digested) {
					return bad("docker." + list.name)
				}
				if list.name == "allowed_images" {
					v = reference.TagNameOnly(ref).String()
				} else {
					v = ref.Name()
				}
			case "allowed_networks":
				if v != "none" && v != "bridge" {
					return bad("docker.allowed_networks")
				}
			case "workspace_roots", "storage_roots":
				if !filepath.IsAbs(v) {
					return bad("docker." + list.name)
				}
				var err error
				v, err = canonicalPath(v)
				if err != nil {
					return bad("docker." + list.name)
				}
			case "allowed_endpoints":
				u, err := url.Parse(v)
				if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !filepath.IsAbs(u.Path) || u.Opaque != "" {
					return bad("docker.allowed_endpoints")
				}
				v = (&url.URL{Scheme: "unix", Path: filepath.Clean(u.Path)}).String()
			}
			if seen[v] {
				return bad("docker." + list.name)
			}
			seen[v] = true
			(*list.values)[i] = v
		}
		slices.Sort(*list.values)
	}
	if d.Authentication == "" {
		return bad("docker.authentication")
	}
	for _, pair := range []struct {
		name string
		l    limits
	}{{"defaults", d.Defaults}, {"ceilings", d.Ceilings}} {
		l := pair.l
		if l.CPUs == nil || *l.CPUs <= 0 || l.Memory == nil || *l.Memory < 6<<20 || l.PIDs == nil || *l.PIDs <= 0 || *l.PIDs > math.MaxInt32 {
			return bad("docker." + pair.name)
		}
	}
	if *d.Defaults.CPUs > *d.Ceilings.CPUs || *d.Defaults.Memory > *d.Ceilings.Memory || *d.Defaults.PIDs > *d.Ceilings.PIDs {
		return bad("docker.defaults")
	}
	return nil
}

// Effective keeps filled resources separate from the original request.
type Effective struct {
	Request      workflow.SandboxRequest `json:"request"`
	NanoCPUs     int64                   `json:"nano_cpus,omitempty"`
	MemoryBytes  int64                   `json:"memory_bytes,omitempty"`
	PIDs         int64                   `json:"pids,omitempty"`
	CPUSource    string                  `json:"cpu_source,omitempty"`
	MemorySource string                  `json:"memory_source,omitempty"`
	PIDsSource   string                  `json:"pids_source,omitempty"`
}

// CheckGuarantees also applies to bare doctor and an empty request set.
func (p *Policy) CheckGuarantees() error {
	if p == nil {
		return nil
	}
	if p.doc.AllowHost == nil || p.doc.Version != 1 {
		return p.failure("sandbox_policy_invalid", "document", "")
	}
	if p.doc.RequireAllAgents {
		return p.failure("sandbox_guarantee_unsupported", "require_all_agents", "")
	}
	if p.doc.HardStorageBytes != nil {
		return p.failure("sandbox_guarantee_unsupported", "hard_storage_bytes", "")
	}
	if p.doc.Docker != nil && p.doc.Docker.Authentication != "none" {
		return p.failure("sandbox_authentication_unsupported", "docker.authentication", "")
	}
	return nil
}

// Authorize implements the engine's consumer-owned seam. Empty storage paths
// mean persistence off, not the current directory.
func (p *Policy) Authorize(st *workflow.Step, workspace, storage []string) error {
	_, err := p.Evaluate(st, workspace, storage, nil)
	return err
}

// Evaluate checks one complete request, optionally retaining previously locked
// effective limits. It never re-defaults a locked value or mutates its input.
func (p *Policy) Evaluate(st *workflow.Step, workspace, storage []string, locked *Effective) (Effective, error) {
	var result Effective
	if st == nil {
		return result, p.failure("sandbox_request_invalid", "request", "")
	}
	r := st.ResolvedSandbox()
	result.Request = r.Clone()
	fail := func(reason, field string) (Effective, error) { return result, p.failure(reason, field, st.ID) }
	if workflow.ValidateSandboxRequest(r) != nil || st.Sandbox != nil && *st.Sandbox != r.Reference {
		return fail("sandbox_request_invalid", "request")
	}
	if err := p.CheckGuarantees(); err != nil {
		return result, err
	}
	// The workflow may live outside the process-entry project. An automatic
	// policy under any of its source/workspace roots is still project input.
	if p != nil && p.source.Kind == "implicit" {
		for _, path := range workspace {
			if path == "" {
				continue
			}
			root, err := canonicalPath(path)
			if err != nil || beneath(root, p.source.Path) {
				return fail("sandbox_policy_unsafe", "source")
			}
		}
	}
	if r.Profile == nil {
		if p != nil && !*p.doc.AllowHost {
			return fail("sandbox_policy_denied", "allow_host")
		}
		return result, nil
	}
	if !p.HasDocker() {
		return fail("sandbox_policy_denied", "docker")
	}
	d := p.doc.Docker
	profile := r.Profile
	ref, err := reference.ParseNormalizedNamed(profile.NormalizedImage)
	if err != nil {
		return fail("sandbox_request_invalid", "image")
	}
	if !slices.Contains(d.Images, profile.NormalizedImage) && !slices.Contains(d.Repositories, ref.Name()) {
		return fail("sandbox_policy_denied", "docker.allowed_images")
	}
	if _, ok := ref.(reference.Digested); d.Digest && !ok {
		return fail("sandbox_policy_denied", "docker.require_digest")
	}
	if !slices.Contains(d.Networks, string(profile.Network)) {
		return fail("sandbox_policy_denied", "docker.allowed_networks")
	}
	if len(st.Secrets) > 0 || st.Type == workflow.StepAgent {
		return fail("sandbox_authentication_unsupported", "docker.authentication")
	}
	result.NanoCPUs = int64(*d.Defaults.CPUs)
	result.MemoryBytes = int64(*d.Defaults.Memory)
	result.PIDs = *d.Defaults.PIDs
	result.CPUSource = "policy_default"
	result.MemorySource = "policy_default"
	result.PIDsSource = "policy_default"
	if profile.CPUs != nil {
		result.NanoCPUs = int64(*profile.CPUs)
		result.CPUSource = "authored"
	}
	if profile.Memory != nil {
		result.MemoryBytes = int64(*profile.Memory)
		result.MemorySource = "authored"
	}
	if profile.PIDs != nil {
		result.PIDs = *profile.PIDs
		result.PIDsSource = "authored"
	}
	if locked != nil {
		a, _ := json.Marshal(locked.Request)
		b, _ := json.Marshal(r)
		if string(a) != string(b) {
			return fail("sandbox_request_invalid", "locked_request")
		}
		result = *locked
		result.Request = r.Clone()
		for _, source := range []string{result.CPUSource, result.MemorySource, result.PIDsSource} {
			if source != "authored" && source != "policy_default" {
				return fail("sandbox_request_invalid", "locked_resources")
			}
		}
		if profile.CPUs != nil && result.NanoCPUs != int64(*profile.CPUs) || profile.Memory != nil && result.MemoryBytes != int64(*profile.Memory) || profile.PIDs != nil && result.PIDs != *profile.PIDs {
			return fail("sandbox_request_invalid", "locked_resources")
		}
	}
	if result.NanoCPUs <= 0 || result.MemoryBytes < 6<<20 || result.PIDs <= 0 || result.PIDs > math.MaxInt32 {
		return fail("sandbox_request_invalid", "resources")
	}
	if result.NanoCPUs > int64(*d.Ceilings.CPUs) {
		return fail("sandbox_policy_denied", "docker.ceilings.cpus")
	}
	if result.MemoryBytes > int64(*d.Ceilings.Memory) {
		return fail("sandbox_policy_denied", "docker.ceilings.memory")
	}
	if result.PIDs > *d.Ceilings.PIDs {
		return fail("sandbox_policy_denied", "docker.ceilings.pids")
	}
	for _, group := range []struct {
		field        string
		paths, roots []string
	}{{"docker.workspace_roots", workspace, d.WorkspaceRoots}, {"docker.storage_roots", storage, d.StorageRoots}} {
		for _, path := range group.paths {
			if path == "" {
				continue
			}
			canonical, err := canonicalPath(path)
			allowed := false
			if err == nil {
				for _, root := range group.roots {
					if beneath(root, canonical) {
						allowed = true
						break
					}
				}
			}
			if !allowed {
				return fail("sandbox_policy_denied", group.field)
			}
		}
	}
	return result, nil
}

func beneath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// canonicalPath resolves existing ancestors, including links, before appending
// missing descendants. Dangling links and non-directory ancestors fail closed.
func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path required")
	}
	path = filepath.Clean(path)
	var tail []string
	for {
		info, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			if len(tail) > 0 {
				info, err = os.Stat(resolved)
				if err != nil || !info.IsDir() {
					return "", fmt.Errorf("invalid ancestor")
				}
			}
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) || path == filepath.Dir(path) {
			return "", err
		}
		tail = append(tail, filepath.Base(path))
		path = filepath.Dir(path)
	}
}

// Reload follows a previously selected file without falling back to discovery.
// Builtin origins deliberately rediscover the current operator default.
func Reload(selection Selection, source Source) (*Policy, error) {
	if selection.ExplicitPath != "" || source.Kind == "builtin" || source.Kind == "" {
		return Load(selection)
	}
	selection.ExplicitPath = source.Path
	p, err := Load(selection)
	if err != nil {
		return nil, err
	}
	p.source.Kind = source.Kind
	return p, nil
}
func (f *Failure) SandboxReason() string { return f.Reason }
