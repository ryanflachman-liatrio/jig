package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/distribution/reference"
)

// SandboxCPU is an exact positive count of nano-CPUs, decoded from TOML CPUs.
// A pointer to it distinguishes an omitted limit from an explicit zero.
type SandboxCPU int64

func (n *SandboxCPU) UnmarshalTOML(v any) error {
	var raw string
	switch x := v.(type) {
	case int64:
		raw = strconv.FormatInt(x, 10)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Errorf("cpus must be finite and positive")
		}
		raw = strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Errorf("cpus must be a TOML integer or float")
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok {
		return fmt.Errorf("invalid cpus")
	}
	r.Mul(r, big.NewRat(1_000_000_000, 1))
	if r.Sign() <= 0 || !r.IsInt() || !r.Num().IsInt64() {
		return fmt.Errorf("cpus must fit positive int64 nano-CPUs exactly")
	}
	*n = SandboxCPU(r.Num().Int64())
	return nil
}

// SandboxMemory is bytes decoded from the author-facing exact memory grammar.
type SandboxMemory int64

var sandboxMemoryPattern = regexp.MustCompile(`^([0-9]+)(KiB|MiB|GiB|TiB)?$`)

func (m *SandboxMemory) UnmarshalTOML(v any) error {
	raw, ok := v.(string)
	if !ok {
		return fmt.Errorf("memory must be a byte-size string")
	}
	parts := sandboxMemoryPattern.FindStringSubmatch(raw)
	if parts == nil {
		return fmt.Errorf("invalid memory size grammar")
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	shift := map[string]uint{"": 0, "KiB": 10, "MiB": 20, "GiB": 30, "TiB": 40}[parts[2]]
	if err != nil || n <= 0 || n > math.MaxInt64>>shift || n<<shift < 6<<20 {
		return fmt.Errorf("memory must be at least 6 MiB and fit int64 bytes")
	}
	*m = SandboxMemory(n << shift)
	return nil
}

// These decoders preserve the distinction between omission and an invalid
// explicit empty enum/platform value, without exporting presence machinery.
type SandboxPull string

func (v *SandboxPull) UnmarshalTOML(raw any) error {
	s, ok := raw.(string)
	if !ok || (s != "always" && s != "if_missing" && s != "never") {
		return fmt.Errorf("invalid sandbox pull policy")
	}
	*v = SandboxPull(s)
	return nil
}

type SandboxNetwork string

func (v *SandboxNetwork) UnmarshalTOML(raw any) error {
	s, ok := raw.(string)
	if !ok || (s != "none" && s != "bridge") {
		return fmt.Errorf("invalid sandbox network")
	}
	*v = SandboxNetwork(s)
	return nil
}

type SandboxPlatform string

func (v *SandboxPlatform) UnmarshalTOML(raw any) error {
	s, ok := raw.(string)
	if !ok || !sandboxPlatformPattern.MatchString(s) {
		return fmt.Errorf("invalid sandbox platform syntax")
	}
	*v = SandboxPlatform(s)
	return nil
}

// SandboxProfile is one complete authored selection; fields never merge across
// profiles. Image retains author input while NormalizedImage is used for policy.
type SandboxProfile struct {
	Name            string            `toml:"name" json:"name"`
	Runtime         string            `toml:"runtime" json:"runtime"`
	Image           string            `toml:"image" json:"image"`
	NormalizedImage string            `toml:"-" json:"normalized_image"`
	Platform        SandboxPlatform   `toml:"platform" json:"platform,omitempty"`
	Pull            SandboxPull       `toml:"pull" json:"pull"`
	Network         SandboxNetwork    `toml:"network" json:"network"`
	CPUs            *SandboxCPU       `toml:"cpus" json:"nano_cpus,omitempty"`
	Memory          *SandboxMemory    `toml:"memory" json:"memory_bytes,omitempty"`
	PIDs            *int64            `toml:"pids" json:"pids,omitempty"`
	Env             map[string]string `toml:"env" json:"env,omitempty"`
}

// SandboxRequest is locked authored intent, not permission or an inspected
// deployment. Origin names the winning lexical selection; source and invocation
// identify its declaration even when an inherited name collides in a child.
type SandboxRequest struct {
	Reference    string          `json:"reference"`
	Origin       string          `json:"origin"`
	Source       string          `json:"source"`
	SourceSHA256 string          `json:"source_sha256"`
	Invocation   string          `json:"invocation"`
	Profile      *SandboxProfile `json:"profile,omitempty"`
}

func clonePointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
func (r SandboxRequest) Clone() SandboxRequest {
	if r.Profile != nil {
		p := *r.Profile
		p.CPUs, p.Memory, p.PIDs = clonePointer(p.CPUs), clonePointer(p.Memory), clonePointer(p.PIDs)
		p.Env = maps.Clone(p.Env)
		r.Profile = &p
	}
	return r
}

// ResolvedSandbox returns an owned copy. Nil metadata on a manually constructed
// host step retains host behavior; unresolved explicit profiles never mean host.
func (s *Step) ResolvedSandbox() SandboxRequest {
	if s.SnapshotSandbox != nil {
		return s.SnapshotSandbox.Clone()
	}
	return SandboxRequest{Reference: "host", Origin: "implicit"}
}

var sandboxNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var sandboxEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var sandboxImageIDPattern = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)
var sandboxPlatformPattern = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+(?:/[a-z0-9]+)?$`)

func normalizeSandboxProfile(p *SandboxProfile) error {
	if !sandboxNamePattern.MatchString(p.Name) || p.Name == "host" {
		return fmt.Errorf("invalid or reserved sandbox name")
	}
	if p.Runtime != "docker" {
		return fmt.Errorf("sandbox runtime must be docker")
	}
	if p.Image == "" || sandboxImageIDPattern.MatchString(p.Image) || strings.Contains(p.Image, "://") || strings.IndexFunc(p.Image, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid sandbox image reference")
	}
	ref, err := reference.ParseNormalizedNamed(p.Image)
	if err != nil {
		return fmt.Errorf("invalid sandbox image reference")
	}
	if _, tagged := ref.(reference.Tagged); tagged {
		if _, digested := ref.(reference.Digested); digested {
			return fmt.Errorf("sandbox image cannot combine tag and digest")
		}
	}
	p.NormalizedImage = reference.TagNameOnly(ref).String()
	if p.Platform != "" && !sandboxPlatformPattern.MatchString(string(p.Platform)) {
		return fmt.Errorf("invalid sandbox platform syntax")
	}
	if p.Pull == "" {
		p.Pull = "if_missing"
	}
	if p.Pull != "if_missing" && p.Pull != "always" && p.Pull != "never" {
		return fmt.Errorf("invalid sandbox pull policy")
	}
	if p.Network == "" {
		p.Network = "none"
	}
	if p.Network != "none" && p.Network != "bridge" {
		return fmt.Errorf("invalid sandbox network")
	}
	if p.CPUs != nil && *p.CPUs <= 0 {
		return fmt.Errorf("invalid sandbox cpus")
	}
	if p.Memory != nil && *p.Memory < 6<<20 {
		return fmt.Errorf("invalid sandbox memory")
	}
	if p.PIDs != nil && (*p.PIDs <= 0 || *p.PIDs > math.MaxInt32) {
		return fmt.Errorf("sandbox pids must be positive int32")
	}
	if len(p.Env) > 128 {
		return fmt.Errorf("sandbox env exceeds key count")
	}
	total := 0
	for key, value := range p.Env {
		if len(key) > 128 || !sandboxEnvPattern.MatchString(key) || len(value) > 8<<10 || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid sandbox env name, value or size")
		}
		switch key {
		case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR":
			return fmt.Errorf("protected sandbox env name")
		}
		// JIG_ also covers normalized JIG_SECRET_* delivery names, without secret I/O.
		for _, prefix := range []string{"JIG_", "DOCKER_", "LD_", "DYLD_"} {
			if strings.HasPrefix(key, prefix) {
				return fmt.Errorf("protected sandbox env prefix")
			}
		}
		total += len(key) + len(value)
	}
	if total > 64<<10 {
		return fmt.Errorf("sandbox env exceeds total bytes")
	}
	return nil
}

func (wf *Workflow) validateSandboxDeclarations() error {
	if len(wf.Sandboxes) > 128 {
		return fmt.Errorf("sandbox profile limit exceeded")
	}
	seen := map[string]bool{}
	for i := range wf.Sandboxes {
		p := &wf.Sandboxes[i]
		if err := normalizeSandboxProfile(p); err != nil {
			return fmt.Errorf("sandbox declaration %d: %w", i+1, err)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate sandbox profile")
		}
		seen[p.Name] = true
	}
	check := func(ref *string) error {
		if ref != nil && *ref != "host" && !seen[*ref] {
			return fmt.Errorf("sandbox reference is empty or unresolved")
		}
		return nil
	}
	if err := check(wf.Defaults.Sandbox); err != nil {
		return fmt.Errorf("defaults: %w", err)
	}
	for _, st := range wf.Steps {
		if err := check(st.Sandbox); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
	}
	return nil
}

func (wf *Workflow) sandboxSelection(ref string, origin, prefix string) SandboxRequest {
	sum := sha256.Sum256([]byte(wf.sourceTOML))
	r := SandboxRequest{Reference: ref, Origin: origin, Source: wf.sourcePath, SourceSHA256: hex.EncodeToString(sum[:]), Invocation: prefix}
	for i := range wf.Sandboxes {
		if wf.Sandboxes[i].Name == ref {
			r.Profile = &wf.Sandboxes[i]
			break
		}
	}
	return r.Clone()
}

// resolveSandboxes carries invocation and ordinary-default inputs separately:
// collapsing them would let a module default override an explicit invocation.
func (wf *Workflow) resolveSandboxes(prefix string, invocation, inherited *SandboxRequest) *SandboxRequest {
	ordinary := inherited
	if wf.Defaults.Sandbox != nil {
		r := wf.sandboxSelection(*wf.Defaults.Sandbox, "default", prefix)
		ordinary = &r
	}
	for i := range wf.Steps {
		st := &wf.Steps[i]
		r := wf.sandboxSelection("host", "implicit", prefix)
		if ordinary != nil {
			r = ordinary.Clone()
			r.Origin = "inherited_default"
			if wf.Defaults.Sandbox != nil {
				r.Origin = "default"
			}
		}
		if invocation != nil {
			r = invocation.Clone()
			r.Origin = "invocation"
		}
		if st.Sandbox != nil {
			r = wf.sandboxSelection(*st.Sandbox, "explicit", prefix)
		}
		st.SnapshotSandbox = &r
	}
	return ordinary
}

// ValidateSandboxRequest checks stored normalized data without resolving any
// source or consulting operator policy. It is also used at runtime boundaries.
func ValidateSandboxRequest(r SandboxRequest) error {
	if r.SourceSHA256 != "" && !validAssetDigest(r.SourceSHA256) {
		return fmt.Errorf("invalid sandbox source digest")
	}
	switch r.Origin {
	case "implicit", "explicit", "default", "inherited_default", "invocation":
	default:
		return fmt.Errorf("invalid sandbox origin")
	}
	if r.Reference == "host" {
		if r.Profile != nil {
			return fmt.Errorf("host request has a profile")
		}
		return nil
	}
	if r.Profile == nil || r.Reference != r.Profile.Name || !validAssetDigest(r.SourceSHA256) {
		return fmt.Errorf("missing sandbox profile")
	}
	p := *r.Profile
	if err := normalizeSandboxProfile(&p); err != nil {
		return err
	}
	if p.NormalizedImage != r.Profile.NormalizedImage || p.Pull != r.Profile.Pull || p.Network != r.Profile.Network {
		return fmt.Errorf("sandbox request is not normalized")
	}
	return nil
}
