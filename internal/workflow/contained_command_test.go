package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestD05WorkspaceRequirements(t *testing.T) {
	for _, tc := range []struct{ declaration, want string }{
		{"", ""}, {"workspace_requirements=[]", ""},
		{"workspace_requirements=['source_history','submodules','lfs']", ""},
		{"workspace_requirements=['']", "unknown workspace_requirements"},
		{"workspace_requirements=['history']", "unknown workspace_requirements"},
		{"workspace_requirements=['lfs','lfs']", "duplicate workspace_requirements"},
		{"workspace_requirements='lfs'", ""},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			wf, err := Decode(d04Header+d04Profile+d04Step+tc.declaration, "")
			if tc.want != "" || tc.declaration == "workspace_requirements='lfs'" {
				if err == nil || tc.want != "" && !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want rejection %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			public := wf.PublicSteps()
			data, err := json.Marshal(public)
			if err != nil {
				t.Fatal(err)
			}
			var restored []Step
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored[0].WorkspaceRequirements, wf.Steps[0].WorkspaceRequirements) {
				t.Fatal("requirements changed during snapshot")
			}
			if len(public[0].WorkspaceRequirements) > 0 {
				public[0].WorkspaceRequirements[0] = "changed"
				if wf.Steps[0].WorkspaceRequirements[0] != "source_history" {
					t.Fatal("requirements alias public graph")
				}
			}
		})
	}
}

func TestD05ContainerFileInputs(t *testing.T) {
	base := d04Header + d04Profile + `pull='never'
[[step]]
id='producer'
type='command'
run='true'
output='out/jig'
[[step]]
id='consumer'
type='command'
run='true'
sandbox='box'
depends_on=['producer']
`
	for _, tc := range []struct{ inputs, want string }{
		{`[{ref='@producer',as='binary'}]`, ""},
		{`['@producer']`, "requires a nonempty alias"},
		{`[{ref='@producer',as='a-b'},{ref='@producer',as='a_b'}]`, "aliases collide"},
		{`[{ref='@producer',as='binary',inline=true}]`, "whole command output"},
		{`[{path='file',as='binary'}]`, "whole command output"},
		{`[{ref='@producer.field',as='binary'}]`, "whole command output"},
	} {
		t.Run(tc.inputs, func(t *testing.T) {
			_, err := Decode(base+"inputs="+tc.inputs, "")
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	for _, replacement := range []struct{ old, new, want string }{
		{"output='out/jig'", "", "single declared command file output"},
		{"depends_on=['producer']", "", "must also appear in depends_on"},
	} {
		_, err := Decode(strings.Replace(base, replacement.old, replacement.new, 1)+`inputs=[{ref='@producer',as='binary'}]`, "")
		if err == nil || !strings.Contains(err.Error(), replacement.want) {
			t.Fatalf("got %v, want %q", err, replacement.want)
		}
	}
}

func TestD05WorkspaceRequirementsModule(t *testing.T) {
	root := t.TempDir()
	child := "[module]\nschema_version=1\n[module.exports.result]\nref='@work'\n" + "[[step]]\nid='work'\ntype='command'\nrun='true'\nworkspace_requirements=['lfs']\n"
	if err := os.WriteFile(filepath.Join(root, "child.toml"), []byte(child), 0600); err != nil {
		t.Fatal(err)
	}
	wf, err := Decode(d04Header+"[[step]]\nid='nested'\ntype='subworkflow'\nmodule='child.toml'\n", root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wf.Steps[0].WorkspaceRequirements, []string{"lfs"}) {
		t.Fatal("module requirements lost")
	}
}

func TestD05BuildFixture(t *testing.T) {
	wf, err := Load(filepath.Join("..", "..", "examples", "docker-build-jig.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Steps) != 2 || wf.Steps[1].Inputs[0].As != "binary" {
		t.Fatal("build/consume contract missing")
	}
}
