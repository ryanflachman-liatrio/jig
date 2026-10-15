package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const d04Header = "[workflow]\nname='sandbox-fixture'\nversion='1'\n"
const d04Profile = "[[sandbox]]\nname='box'\nruntime='docker'\nimage='alpine'\n"
const d04Step = "[[step]]\nid='work'\ntype='command'\nrun='true'\nsandbox='box'\n"

func TestD04OfflineProfile(t *testing.T) {
	wf, err := Decode(d04Header+d04Profile+"cpus=0.125\nmemory='6MiB'\npids=1\n[sandbox.env]\nCOLOR='${LITERAL}'\n"+d04Step, "")
	if err != nil {
		t.Fatal(err)
	}
	r := wf.Steps[0].ResolvedSandbox()
	if r.Reference != "box" || r.Origin != "explicit" || len(r.SourceSHA256) != 64 || r.Profile.NormalizedImage != "docker.io/library/alpine:latest" || *r.Profile.CPUs != 125000000 || *r.Profile.Memory != 6<<20 || *r.Profile.PIDs != 1 || r.Profile.Env["COLOR"] != "${LITERAL}" || r.Profile.Pull != "if_missing" || r.Profile.Network != "none" {
		t.Fatalf("unexpected request: %+v / %+v", r, r.Profile)
	}
	r.Profile.Env["COLOR"] = "mutated"
	*r.Profile.CPUs = 2
	if wf.Steps[0].ResolvedSandbox().Profile.Env["COLOR"] != "${LITERAL}" || *wf.Steps[0].ResolvedSandbox().Profile.CPUs != 125000000 {
		t.Fatal("request accessor aliases")
	}
	public := wf.PublicSteps()
	public[0].SnapshotSandbox.Profile.Env["COLOR"] = "public mutation"
	if wf.Steps[0].ResolvedSandbox().Profile.Env["COLOR"] != "${LITERAL}" {
		t.Fatal("public graph aliases request")
	}
	for _, kind := range []string{"command", "check", "agent", "review"} {
		t.Run(kind, func(t *testing.T) {
			fields := map[string]string{"command": "run='true'", "check": "run='true'\napplies_when='ready'\ndepends_on=['ready']\noutput_type={enum=['pass','fail','skip','error']}\n[step.findings]\nschema_version=1\nfile='findings.json'\nrequired_tools=['true']\n[[step.route]]\nwhen=\"work != 'pass'\"\ngoto='work'\nmax_iterations=2", "agent": "skill='synthetic-skill'", "review": "output_type='bool'\n[[step.review]]\nsource='diff'\nlabel='Changes'"}[kind]
			_, err := Decode(d04Header+d04Profile+"[[step]]\nid='ready'\ntype='command'\nrun='true'\noutput_type='bool'\n[[step]]\nid='work'\ntype='"+kind+"'\nsandbox='box'\n"+fields, "")
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestD04OfflineInvalidProfiles(t *testing.T) {
	tests := map[string]string{
		"pull_empty": "pull=''", "network_empty": "network=''", "platform_empty": "platform=''",
		"name_empty": "name=''", "name_reserved": "name='host'", "name_digit": "name='1bad'", "name_long": "name='" + strings.Repeat("a", 65) + "'",
		"runtime_missing": "runtime=''", "runtime_host": "runtime='host'", "unknown": "raw_flags='--privileged'",
		"image_empty": "image=''", "image_url": "image='https://example.test/a'", "image_credentials": "image='user:private@example.test/a'", "image_bare_id": "image='" + strings.Repeat("a", 64) + "'", "image_sha_id": "image='sha256:" + strings.Repeat("a", 64) + "'", "image_tag_digest": "image='alpine:latest@sha256:" + strings.Repeat("a", 64) + "'",
		"platform": "platform='linux'", "pull": "pull='sometimes'", "network": "network='host'",
		"cpu_zero": "cpus=0", "cpu_negative": "cpus=-1", "cpu_string": "cpus='1'", "cpu_nan": "cpus=nan", "cpu_inf": "cpus=inf", "cpu_subnano": "cpus=0.0000000001", "cpu_overflow": "cpus=9223372037",
		"memory_int": "memory=6291456", "memory_small": "memory='6291455'", "memory_fraction": "memory='6.5MiB'", "memory_decimal": "memory='6MB'", "memory_space": "memory='6 MiB'", "memory_sign": "memory='+6MiB'", "memory_overflow": "memory='8388608TiB'", "memory_zero": "memory='0'",
		"pids_zero": "pids=0", "pids_overflow": "pids=2147483648", "pids_fraction": "pids=1.5",
		"env_type": "env={A=1}", "env_name": "env={'bad-name'='x'}", "env_nul": "env={A=\"\\u0000\"}", "env_value_size": "env={A='" + strings.Repeat("x", 8193) + "'}", "env_key_size": "env={'" + strings.Repeat("A", 129) + "'='x'}",
	}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "JIG_SECRET_A", "DOCKER_HOST", "LD_PRELOAD", "DYLD_TEST"} {
		tests["protected_"+key] = "env={" + key + "='NEVER_DISPLAY_THIS_VALUE'}"
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			profile := d04Profile
			field := strings.SplitN(change, "=", 2)[0]
			replaced := false
			lines := strings.Split(profile, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, field+"=") {
					lines[i] = change
					replaced = true
				}
			}
			profile = strings.Join(lines, "\n")
			if !replaced {
				profile += change + "\n"
			}
			// Invalid profiles must be rejected even when unused and a host step is skipped.
			_, err := Decode(d04Header+profile+strings.Replace(d04Step, "sandbox='box'", "sandbox='host'\nwhen='false'", 1), "")
			if err == nil {
				t.Fatal("invalid unused profile accepted")
			}
			if strings.Contains(err.Error(), "NEVER_DISPLAY_THIS_VALUE") {
				t.Fatal("environment value leaked")
			}
		})
	}
	for _, selection := range []string{"", "missing", "namespace__box"} {
		if _, err := Decode(d04Header+d04Profile+strings.Replace(d04Step, "sandbox='box'", "sandbox='"+selection+"'", 1), ""); err == nil {
			t.Fatalf("selection %q accepted", selection)
		}
	}
	if _, err := Decode(d04Header+d04Profile+d04Profile+d04Step, ""); err == nil {
		t.Fatal("duplicate profile accepted")
	}
}

func TestD04OfflineBounds(t *testing.T) {
	for _, fields := range []string{"cpus=0.000000001", "cpus=9223372036", "memory='9223372036854775807'", "memory='6MiB'", "pids=2147483647", "env={A='" + strings.Repeat("x", 8192) + "'}", "env={'" + strings.Repeat("A", 128) + "'='x'}", "platform='windows/amd64'"} {
		if _, err := Decode(d04Header+d04Profile+fields+"\n"+d04Step, ""); err != nil {
			t.Fatalf("boundary %s: %v", strings.SplitN(fields, "=", 2)[0], err)
		}
	}
	for _, count := range []int{128, 129} {
		var profiles, env strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&profiles, "[[sandbox]]\nname='p%d'\nruntime='docker'\nimage='alpine'\n", i)
			fmt.Fprintf(&env, "K%d='x'\n", i)
		}
		_, err := Decode(d04Header+profiles.String()+strings.Replace(d04Step, "'box'", "'p0'", 1), "")
		if (err == nil) != (count == 128) {
			t.Fatalf("profile count %d: %v", count, err)
		}
		_, err = Decode(d04Header+d04Profile+"[sandbox.env]\n"+env.String()+d04Step, "")
		if (err == nil) != (count == 128) {
			t.Fatalf("env count %d: %v", count, err)
		}
	}
	// Eight keys + values total exactly 64 KiB; one extra byte is rejected.
	for _, extra := range []int{0, 1} {
		var env strings.Builder
		for i := 0; i < 8; i++ {
			n := 8191
			if i == 0 {
				n += extra
			}
			fmt.Fprintf(&env, "%c='%s'\n", 'A'+i, strings.Repeat("x", n))
		}
		_, err := Decode(d04Header+d04Profile+"[sandbox.env]\n"+env.String()+d04Step, "")
		if (err == nil) != (extra == 0) {
			t.Fatalf("aggregate boundary extra=%d: %v", extra, err)
		}
	}
}

func TestD04OfflineLexicalSelection(t *testing.T) {
	dir := t.TempDir()
	profile := func(image string) string {
		return "[[sandbox]]\nname='box'\nruntime='docker'\nimage='" + image + "'\nenv={ONLY='" + image + "'}\n"
	}
	files := map[string]string{
		"root.toml":  d04Header + profile("root") + "[defaults]\nsandbox='box'\n[[step]]\nid='ordinary'\ntype='subworkflow'\nmodule='child.toml'\n[[step]]\nid='forced'\ntype='subworkflow'\nmodule='child.toml'\nsandbox='box'\n[[step]]\nid='inherited'\ntype='subworkflow'\nmodule='plain.toml'\n",
		"child.toml": "[module]\nschema_version=1\n[module.exports.result]\nref='@local'\n" + profile("child") + "[defaults]\nsandbox='box'\n[[step]]\nid='local'\ntype='command'\nrun='true'\n[[step]]\nid='explicit'\ntype='command'\nrun='true'\nsandbox='box'\n[[step]]\nid='host'\ntype='command'\nrun='true'\nsandbox='host'\n[[step]]\nid='nested'\ntype='subworkflow'\nmodule='leaf.toml'\n[[step]]\nid='override'\ntype='subworkflow'\nmodule='leaf.toml'\nsandbox='box'\n",
		"leaf.toml":  "[module]\nschema_version=1\n[module.exports.result]\nref='@work'\n" + profile("leaf") + "[defaults]\nsandbox='box'\n[[step]]\nid='work'\ntype='command'\nrun='true'\n",
		"plain.toml": "[module]\nschema_version=1\n[module.exports.result]\nref='@work'\n[[step]]\nid='work'\ntype='command'\nrun='true'\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	wf, err := Load(filepath.Join(dir, "root.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ordinary__local": "child", "ordinary__explicit": "child", "ordinary__host": "host", "ordinary__nested__work": "leaf", "ordinary__override__work": "child", "forced__local": "root", "forced__explicit": "child", "forced__host": "host", "forced__nested__work": "root", "forced__override__work": "child", "inherited__work": "root"}
	for _, st := range wf.Steps {
		r := st.ResolvedSandbox()
		got := "host"
		if r.Profile != nil {
			got = r.Profile.Image
			if len(r.Profile.Env) != 1 || r.Profile.Env["ONLY"] != got || r.Profile.CPUs != nil {
				t.Fatal("profile fields merged")
			}
		}
		if got != want[st.ID] {
			t.Fatalf("%s got %s want %s", st.ID, got, want[st.ID])
		}
		t.Logf("step=%s selected=%s origin=%s invocation=%s", st.ID, got, r.Origin, r.Invocation)
	}
	first := d04StepNamed(wf, "ordinary__explicit").ResolvedSandbox()
	second := d04StepNamed(wf, "forced__explicit").ResolvedSandbox()
	if first.SourceSHA256 != second.SourceSHA256 || first.Invocation == second.Invocation {
		t.Fatal("repeated import identity is ambiguous")
	}
	copy, err := RestoreExpanded(wf.Meta, wf.Defaults, wf.PublicSteps(), wf.Steps, wf.ModuleSources())
	if err != nil {
		t.Fatal(err)
	}
	before := d04StepNamed(copy, "ordinary__explicit").ResolvedSandbox()
	d04StepNamed(wf, "ordinary__explicit").SnapshotSandbox.Profile.Env["ONLY"] = "changed"
	if !reflect.DeepEqual(before, d04StepNamed(copy, "ordinary__explicit").ResolvedSandbox()) {
		t.Fatal("restored request aliases input")
	}
	plain, err := Decode(d04Header+"[[step]]\nid='work'\ntype='command'\nrun='true'", "")
	if err != nil || plain.Steps[0].ResolvedSandbox().Reference != "host" {
		t.Fatal("implicit host broken", err)
	}
}

func d04StepNamed(wf *Workflow, id string) *Step { return &wf.Steps[wf.index[id]] }
