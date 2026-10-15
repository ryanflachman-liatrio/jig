package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EndpointInput is an owned process-entry selection, never a live environment
// reader. Unused Docker auth/TLS/header/API/platform settings are not captured.
type EndpointInput struct {
	Cwd, Project, Home             string
	Context, Host, Config          string
	EnvContext, EnvHost, EnvConfig string
}

func (in EndpointInput) Explicit() bool { return in.Context != "" || in.Host != "" || in.Config != "" }

// EndpointSummary contains safe provenance, not Docker configuration contents.
type EndpointSummary struct {
	URI            string `json:"uri"`
	Context        string `json:"context,omitempty"`
	ContextSource  string `json:"context_source"`
	EndpointSource string `json:"endpoint_source"`
	ConfigSource   string `json:"config_source,omitempty"`
	ConfigRoot     string `json:"config_root,omitempty"`
}

// Endpoint pins selection and socket identity. Changing environment or context
// files cannot mutate it; loss/replacement fails rather than selecting again.
type Endpoint struct {
	summary  EndpointSummary
	path     string
	identity os.FileInfo
}

func (e *Endpoint) Summary() EndpointSummary {
	if e == nil {
		return EndpointSummary{}
	}
	s := e.summary
	s.URI = Display(s.URI)
	s.Context = Display(s.Context)
	s.ConfigRoot = Display(s.ConfigRoot)
	return s
}
func (e *Endpoint) SocketPath() string {
	if e == nil {
		return ""
	}
	return e.path
}
func (e *Endpoint) CheckBound() error {
	if e == nil {
		return endpointFailure("sandbox_endpoint_unavailable", "binding")
	}
	info, err := os.Stat(e.path)
	if err != nil || info.Mode()&os.ModeSocket == 0 || !os.SameFile(e.identity, info) {
		return endpointFailure("sandbox_endpoint_changed", "binding")
	}
	return nil
}
func endpointFailure(reason, field string) error {
	return &Failure{Reason: reason, Field: field, Source: Source{Kind: "endpoint"}}
}

// SelectEndpoint performs bounded configuration reads and socket metadata
// checks only. It never dials, runs a helper, enumerates stores, or reads auth.
func SelectEndpoint(in EndpointInput, p *Policy) (*Endpoint, error) {
	if err := p.CheckGuarantees(); err != nil {
		return nil, err
	}
	if !p.HasDocker() {
		return nil, p.failure("sandbox_policy_denied", "docker", "")
	}
	if in.Host != "" && in.Context != "" {
		return nil, endpointFailure("sandbox_endpoint_invalid", "flags")
	}
	if !filepath.IsAbs(in.Cwd) {
		return nil, endpointFailure("sandbox_endpoint_invalid", "cwd")
	}
	// Validate only winning inputs. Ambient host/config cannot poison a named
	// context or explicit host that does not consume them.
	summary := EndpointSummary{}
	contextName, host := "", ""
	switch {
	case in.Host != "":
		host = in.Host
		summary.ContextSource = "flag_host"
		summary.EndpointSource = "flag_host"
	case in.Context != "":
		contextName = in.Context
		summary.ContextSource = "flag_context"
	case in.EnvContext != "":
		contextName = in.EnvContext
		summary.ContextSource = "env_context"
	case in.EnvHost != "":
		host = in.EnvHost
		summary.ContextSource = "env_host"
		summary.EndpointSource = "env_host"
	}
	var configRoot string
	configExplicit := in.Config != ""
	resolveConfig := func() error {
		root, source := in.Config, "flag_config"
		if root == "" {
			root = in.EnvConfig
			source = "env_config"
		}
		if root == "" {
			if !filepath.IsAbs(in.Home) {
				return endpointFailure("sandbox_endpoint_invalid", "home")
			}
			root = filepath.Join(in.Home, ".docker")
			source = "home"
		}
		if strings.TrimSpace(root) == "" {
			return endpointFailure("sandbox_endpoint_invalid", "config_root")
		}
		if !filepath.IsAbs(root) {
			if !configExplicit {
				return endpointFailure("sandbox_endpoint_invalid", "config_root")
			}
			root = filepath.Join(in.Cwd, root)
		}
		canonical, err := canonicalPath(root)
		if err != nil {
			return endpointFailure("sandbox_endpoint_unavailable", "config_root")
		}
		if !configExplicit && inProject(in, canonical) {
			return endpointFailure("sandbox_endpoint_denied", "config_root")
		}
		configRoot = canonical
		summary.ConfigRoot = canonical
		summary.ConfigSource = source
		return nil
	}
	if host == "" && contextName == "" {
		if err := resolveConfig(); err != nil {
			return nil, err
		}
		var config configProjection
		err := readProjection(filepath.Join(configRoot, "config.json"), in, configExplicit, &config)
		if err != nil && !(os.IsNotExist(err) && summary.ConfigSource == "home") {
			return nil, endpointFailure("sandbox_endpoint_invalid", "config.json")
		}
		contextName = config.CurrentContext
		if contextName != "" {
			summary.ContextSource = "current_context"
		} else {
			contextName = "default"
			summary.ContextSource = "builtin"
		}
	}
	if contextName != "" {
		if strings.TrimSpace(contextName) == "" || strings.TrimSpace(contextName) != contextName || len(contextName) > 256 || strings.ContainsAny(contextName, "/\\\x00\r\n") {
			return nil, endpointFailure("sandbox_endpoint_invalid", "context")
		}
		summary.Context = contextName
		if contextName == "default" {
			host = "unix:///var/run/docker.sock"
			summary.EndpointSource = "builtin"
			if in.EnvHost != "" {
				host = in.EnvHost
				summary.EndpointSource = "env_host"
			}
		} else {
			if configRoot == "" {
				if err := resolveConfig(); err != nil {
					return nil, err
				}
			}
			hash := sha256.Sum256([]byte(contextName))
			path := filepath.Join(configRoot, "contexts", "meta", hex.EncodeToString(hash[:]), "meta.json")
			var meta contextProjection
			if err := readProjection(path, in, configExplicit, &meta); err != nil || meta.Name != contextName {
				return nil, endpointFailure("sandbox_endpoint_invalid", "context_metadata")
			}
			if meta.SkipTLSVerify {
				return nil, endpointFailure("sandbox_endpoint_unsupported", "tls")
			}
			host = meta.Host
			summary.EndpointSource = "context_metadata"
		}
	}
	path, err := socketPath(host)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, endpointFailure("sandbox_endpoint_unavailable", "socket")
	}
	info, err := os.Stat(canonical)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, endpointFailure("sandbox_endpoint_unavailable", "socket")
	}
	if err := authorizeEndpointPath(canonical, p); err != nil {
		return nil, err
	}
	summary.URI = "unix://" + (&url.URL{Path: canonical}).EscapedPath()
	return &Endpoint{summary: summary, path: canonical, identity: info}, nil
}
func socketPath(raw string) (string, error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", endpointFailure("sandbox_endpoint_unsupported", "platform")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !filepath.IsAbs(u.Path) || strings.ContainsRune(u.Path, 0) {
		return "", endpointFailure("sandbox_endpoint_unsupported", "transport")
	}
	return filepath.Clean(u.Path), nil
}
func inProject(in EndpointInput, path string) bool {
	root := in.Project
	if root == "" {
		root = in.Cwd
	}
	canonical, err := canonicalPath(root)
	return err != nil || beneath(canonical, path)
}
func readProjection(path string, in EndpointInput, explicit bool, out any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("metadata link unavailable")
		}
		return err
	}
	if !explicit && inProject(in, resolved) {
		return fmt.Errorf("automatic project configuration")
	}
	data, err := readMetadata(resolved)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := uniqueJSON(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	// Only this caller's selected fields are decoded into application state.
	return json.Unmarshal(data, out)
}

// uniqueJSON checks duplicate object keys even in ignored Docker-owned fields.
func uniqueJSON(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting limit")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[name] = true
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := uniqueJSON(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid JSON")
	}
	_, err = dec.Token()
	return err
}

type configProjection struct{ CurrentContext string }

func (p *configProjection) UnmarshalJSON(data []byte) error {
	obj, err := projectionObject(data)
	if err != nil {
		return err
	}
	p.CurrentContext, err = projectionString(obj, "currentContext", false)
	return err
}

type contextProjection struct {
	Name, Host    string
	SkipTLSVerify bool
}

func (p *contextProjection) UnmarshalJSON(data []byte) error {
	obj, err := projectionObject(data)
	if err != nil {
		return err
	}
	p.Name, err = projectionString(obj, "Name", true)
	if err != nil {
		return err
	}
	endpoints, err := projectionObject(obj["Endpoints"])
	if err != nil {
		return err
	}
	docker, err := projectionObject(endpoints["docker"])
	if err != nil {
		return err
	}
	p.Host, err = projectionString(docker, "Host", true)
	if err != nil {
		return err
	}
	if raw, ok := docker["SkipTLSVerify"]; ok {
		if string(raw) == "null" {
			return fmt.Errorf("invalid TLS flag")
		}
		if err := json.Unmarshal(raw, &p.SkipTLSVerify); err != nil {
			return err
		}
	}
	return nil
}
func projectionObject(data []byte) (map[string]json.RawMessage, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("object required")
	}
	var result map[string]json.RawMessage
	err := json.Unmarshal(data, &result)
	return result, err
}
func projectionString(obj map[string]json.RawMessage, key string, required bool) (string, error) {
	raw, ok := obj[key]
	if !ok && !required {
		return "", nil
	}
	if !ok || string(raw) == "null" {
		return "", fmt.Errorf("selected string required")
	}
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err
}

func authorizeEndpointPath(canonical string, p *Policy) error {
	for _, entry := range p.Endpoints() {
		candidate, err := socketPath(entry)
		if err != nil {
			continue
		}
		candidate, err = filepath.EvalSymlinks(candidate)
		if err == nil && candidate == canonical {
			return nil
		}
	}
	return p.failure("sandbox_policy_denied", "docker.allowed_endpoints", "")
}
