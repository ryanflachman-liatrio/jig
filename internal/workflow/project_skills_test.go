package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSkillsDisableModelInvocation(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", ".agents", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no project skills found")
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			found := false
			for _, line := range lines[1:] {
				if strings.TrimSpace(line) == "---" {
					break
				}
				if strings.TrimSpace(line) == "disable-model-invocation: true" {
					found = true
				}
			}
			if !found {
				t.Error("workflow skill must disable model invocation because jig injects its instruction body")
			}
		})
	}
}
