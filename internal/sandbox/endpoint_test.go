package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func endpointFixture(t *testing.T) (EndpointInput, *Policy, string) {
	t.Helper()
	policyIn, data := policyFixture(t)
	// Keep socket path short enough for Darwin's Unix-domain path bound.
	dir, err := os.MkdirTemp("/tmp", "jig-ep-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	data = strings.Replace(data, "unix:///synthetic/docker.sock", "unix://"+socket, 1)
	p := loadFixture(t, policyIn, data)
	input := EndpointInput{Cwd: policyIn.Project, Project: policyIn.Project, Home: policyIn.Home, Config: filepath.Join(policyIn.Home, "docker")}
	writeEndpointFile(t, filepath.Join(input.Config, "config.json"), `{"currentContext":"desktop"}`)
	contextFile(t, input.Config, "desktop", `{"Name":"desktop","Endpoints":{"docker":{"Host":"unix://`+socket+`","SkipTLSVerify":false}},"Storage":{"MetadataPath":"ignored"}}`)
	return input, p, socket
}
func writeEndpointFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func contextFile(t *testing.T, root, name, data string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(name))
	path := filepath.Join(root, "contexts", "meta", hex.EncodeToString(hash[:]), "meta.json")
	writeEndpointFile(t, path, data)
	return path
}
func TestD04EndpointPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, context, host, envContext, envHost, source string }{
		{"explicit-host", "", "selected", "bad", "bad", "flag_host"},
		{"explicit-context", "desktop", "", "bad", "bad", "context_metadata"},
		{"environment-context", "", "", "desktop", "bad", "context_metadata"},
		{"environment-host", "", "", "", "selected", "env_host"},
		{"current-context", "", "", "", "", "context_metadata"},
		{"default-context-env-host", "default", "", "", "selected", "env_host"},
		{"env-default", "", "", "default", "selected", "env_host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, p, socket := endpointFixture(t)
			in.Context = tc.context
			in.Host = tc.host
			in.EnvContext = tc.envContext
			in.EnvHost = tc.envHost
			if in.Host == "selected" {
				in.Host = "unix://" + socket
			}
			if in.EnvHost == "selected" {
				in.EnvHost = "unix://" + socket
			}
			ep, err := SelectEndpoint(in, p)
			if err != nil {
				t.Fatal(err)
			}
			if ep.Summary().EndpointSource != tc.source {
				t.Fatalf("summary=%+v", ep.Summary())
			}
			before := ep.Summary()
			writeEndpointFile(t, filepath.Join(in.Config, "config.json"), `{"currentContext":"changed"}`)
			in.EnvHost = "changed"
			if ep.Summary() != before || ep.CheckBound() != nil {
				t.Fatal("selection changed")
			}
			t.Logf("context_source=%s endpoint_source=%s", before.ContextSource, before.EndpointSource)
		})
	}
}
func TestD04EndpointInvalidWinningSources(t *testing.T) {
	for _, tc := range []struct{ name, config string }{
		{"duplicate", `{"currentContext":"desktop","currentContext":"default"}`},
		{"nested-duplicate", `{"auths":{"ignored":{"x":1,"x":2}}}`},
		{"trailing", `{} {}`}, {"wrong-type", `{"currentContext":3}`}, {"null-field", `{"currentContext":null}`}, {"null-root", `null`},
		{"space", `{"currentContext":" "}`}, {"missing-context", `{"currentContext":"missing"}`},
		{"oversize", strings.Repeat(" ", policyLimit) + "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, p, _ := endpointFixture(t)
			writeEndpointFile(t, filepath.Join(in.Config, "config.json"), tc.config)
			if _, err := SelectEndpoint(in, p); err == nil {
				t.Fatal("invalid winner fell back")
			}
		})
	}
	for _, raw := range []string{`{"Name":"different","Endpoints":{"docker":{"Host":"unix:///missing"}}}`, `{"Name":"desktop","Endpoints":{"docker":{"Host":"unix:///missing","SkipTLSVerify":null}}}`, `{"Name":"desktop","Endpoints":{"docker":{"Host":"unix:///missing","SkipTLSVerify":true}}}`} {
		in, p, _ := endpointFixture(t)
		contextFile(t, in.Config, "desktop", raw)
		if _, err := SelectEndpoint(in, p); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	for _, host := range []string{"tcp://localhost:2375", "ssh://operator@host", "http://host", "npipe:////pipe/docker", "unix:relative", "unix://user@host/path", "unix:///tmp/socket?x=1", "unix:///tmp/socket#fragment", " ", "unix:///missing"} {
		in, p, _ := endpointFixture(t)
		in.Host = host
		in.EnvContext = "desktop"
		if _, err := SelectEndpoint(in, p); err == nil {
			t.Fatalf("invalid host %q fell back", host)
		}
	}
}
func TestD04EndpointAuthorityAndBinding(t *testing.T) {
	in, p, socket := endpointFixture(t)
	// CLI v29.3.0 metadata uses SHA256(context name)/meta.json; unrelated
	// login/helper/TLS fields are ignored and no helper is initialized.
	writeEndpointFile(t, filepath.Join(in.Config, "config.json"), `{"currentContext":"desktop","credsStore":"PRIVATE_SENTINEL","auths":{"private.example":{"auth":"PRIVATE_SENTINEL"}}}`)
	ep, err := SelectEndpoint(in, p)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ep.Summary())
	if strings.Contains(string(raw), "PRIVATE_SENTINEL") {
		t.Fatal("credentials leaked")
	}
	link := filepath.Join(filepath.Dir(socket), "link.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	in.Host = "unix://" + link
	linked, err := SelectEndpoint(in, p)
	if err != nil || linked.SocketPath() != ep.SocketPath() {
		t.Fatalf("socket link: %v", err)
	}
	old := socket + ".old"
	if err := os.Rename(socket, old); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if ep.CheckBound() == nil {
		t.Fatal("replaced socket accepted")
	}
	in, p, _ = endpointFixture(t)
	in.EnvConfig = "relative"
	in.Config = ""
	if _, err := SelectEndpoint(in, p); err == nil {
		t.Fatal("relative environment root accepted")
	}
	in.EnvConfig = in.Project
	if _, err := SelectEndpoint(in, p); err == nil {
		t.Fatal("project automatic config accepted")
	}
	in, p, _ = endpointFixture(t)
	in.Config = ""
	in.EnvConfig = ""
	in.EnvContext = "missing"
	if _, err := SelectEndpoint(in, p); err == nil {
		t.Fatal("missing named context fell back")
	}
	// Policy failure precedes even poisoned configuration parsing.
	in.Config = "PRIVATE_SENTINEL"
	_, err = SelectEndpoint(in, nil)
	requireReason(t, err, "sandbox_policy_denied", "docker")
}

func TestD04EndpointConfigRootsAndAllowlist(t *testing.T) {
	for _, source := range []string{"flag_config", "env_config", "home"} {
		t.Run(source, func(t *testing.T) {
			in, p, socket := endpointFixture(t)
			root := in.Config
			if source == "flag_config" {
				relative, err := filepath.Rel(in.Cwd, in.Config)
				if err != nil {
					t.Fatal(err)
				}
				in.Config = relative
				in.EnvConfig = "relative-invalid"
			}
			if source == "env_config" {
				in.EnvConfig = in.Config
				in.Config = ""
			}
			if source == "home" {
				root = filepath.Join(in.Home, ".docker")
				writeEndpointFile(t, filepath.Join(root, "config.json"), `{"currentContext":"desktop"}`)
				contextFile(t, root, "desktop", `{"Name":"desktop","Endpoints":{"docker":{"Host":"unix://`+socket+`"}}}`)
				in.Config = ""
			}
			ep, err := SelectEndpoint(in, p)
			if err != nil {
				t.Fatal(err)
			}
			if ep.Summary().ConfigSource != source {
				t.Fatalf("summary=%+v", ep.Summary())
			}
		})
	}
	in, p, socket := endpointFixture(t)
	in.Host = "unix://" + socket
	policyInput, data := policyFixture(t)
	data = strings.Replace(data, `["unix:///synthetic/docker.sock"]`, `["unix:///unrelated/missing.sock", "unix://`+socket+`"]`, 1)
	p = loadFixture(t, policyInput, data)
	if _, err := SelectEndpoint(in, p); err != nil {
		t.Fatal("unavailable unrelated allowlist entry", err)
	}
	p = loadFixture(t, policyInput, strings.Replace(data, `, "unix://`+socket+`"`, "", 1))
	_, err := SelectEndpoint(in, p)
	requireReason(t, err, "sandbox_policy_denied", "docker.allowed_endpoints")
	// A dangling present config link is not absent default configuration.
	in, p, _ = endpointFixture(t)
	in.Config = ""
	root := filepath.Join(in.Home, ".docker")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "config.json")); err != nil {
		t.Fatal(err)
	}
	_, err = SelectEndpoint(in, p)
	requireReason(t, err, "sandbox_endpoint_invalid", "config.json")
}
