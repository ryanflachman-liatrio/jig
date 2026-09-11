package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"jig/internal/workflow"
)

// Options selects the target and embedded template for a scaffold plan.
type Options struct {
	Dir      string
	Name     string
	Template string
}

// PlannedFile is one filesystem mutation computed before any writes occur.
// Append is used for .gitignore so applying a plan need not rewrite it.
type PlannedFile struct {
	Path     string
	Contents []byte
	Exists   bool
	Append   bool
}

// WritePlan is the complete ordered set of filesystem mutations for a
// scaffold. It is named separately because Go cannot declare a type and a
// function both named Plan.
type WritePlan struct {
	TargetDir    string
	Name         string
	Template     Template
	WorkflowPath string
	Files        []PlannedFile
}

// Collisions returns non-append targets that already exist, in write order.
// A .gitignore append is deliberately excluded because it is idempotent and
// never replaces its existing contents.
func (plan *WritePlan) Collisions() []string {
	if plan == nil {
		return nil
	}

	var collisions []string
	for _, file := range plan.Files {
		if file.Exists && !file.Append {
			collisions = append(collisions, file.Path)
		}
	}
	return collisions
}

// WrittenFile records one completed write and whether it replaced an existing
// non-append target.
type WrittenFile struct {
	Path        string
	Overwritten bool
}

// Result records completed writes in plan order. It is returned even when a
// later write fails so callers can report partial progress.
type Result struct {
	WorkflowPath string
	Files        []WrittenFile
}

// Plan renders a complete scaffold into memory without modifying the target.
func Plan(options Options) (*WritePlan, error) {
	target := options.Dir
	if target == "" {
		target = "."
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target directory: %w", err)
	}
	target = filepath.Clean(target)

	name := options.Name
	if name == "" {
		name = DefaultName(target)
	}
	name, err = ValidateName(name)
	if err != nil {
		return nil, err
	}

	templateName := options.Template
	if templateName == "" {
		templateName = "minimal"
	}
	selected, err := Lookup(templateName)
	if err != nil {
		return nil, err
	}

	workflowPath := filepath.Join(target, ".agents", "jig", name+".toml")
	workflowContents, err := renderTemplateAsset(path.Join(selected.AssetDir, "workflow.toml.tmpl"), templateData{Name: name})
	if err != nil {
		return nil, err
	}

	result := &WritePlan{
		TargetDir:    target,
		Name:         name,
		Template:     selected,
		WorkflowPath: workflowPath,
	}
	if err := result.addFile(workflowPath, workflowContents, false); err != nil {
		return nil, err
	}

	for _, skillRef := range selected.SkillRefs {
		skillName := path.Base(filepath.ToSlash(skillRef))
		assetPath := path.Join(selected.AssetDir, "skills", skillName, "SKILL.md.tmpl")
		contents, err := renderTemplateAsset(assetPath, templateData{Name: name})
		if err != nil {
			return nil, err
		}
		skillPath := filepath.Join(filepath.Dir(workflowPath), filepath.FromSlash(skillRef), "SKILL.md")
		if err := result.addFile(skillPath, contents, false); err != nil {
			return nil, err
		}
	}

	gitignorePath := filepath.Join(target, ".gitignore")
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", gitignorePath, err)
	}
	if NeedsJigIgnore(existing) {
		updated := AppendJigIgnore(existing)
		appendContents := updated[len(existing):]
		if err := result.addFile(gitignorePath, appendContents, true); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (plan *WritePlan) addFile(filePath string, contents []byte, appendOnly bool) error {
	cleaned := filepath.Clean(filePath)
	if err := ensureInside(plan.TargetDir, cleaned); err != nil {
		return err
	}

	_, err := os.Lstat(cleaned)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect planned path %s: %w", cleaned, err)
	}
	plan.Files = append(plan.Files, PlannedFile{
		Path:     cleaned,
		Contents: append([]byte(nil), contents...),
		Exists:   exists,
		Append:   appendOnly,
	})
	return nil
}

func ensureInside(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return fmt.Errorf("verify planned path %s: %w", candidate, err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("planned path %s escapes target directory %s", candidate, root)
	}
	return nil
}

// Apply writes every planned file in order. Collision reporting is owned by
// the caller; this method still fails closed if a non-append target exists and
// force is false.
func (plan *WritePlan) Apply(force bool) (*Result, error) {
	result := &Result{WorkflowPath: plan.WorkflowPath}
	for _, file := range plan.Files {
		if err := ensureInside(plan.TargetDir, file.Path); err != nil {
			return result, err
		}
		if file.Exists && !file.Append && !force {
			return result, fmt.Errorf("refusing to overwrite %s without force", file.Path)
		}
		if err := os.MkdirAll(filepath.Dir(file.Path), 0o755); err != nil {
			return result, fmt.Errorf("create parent directory for %s: %w", file.Path, err)
		}
		if err := writePlannedFile(file, force); err != nil {
			return result, err
		}
		result.Files = append(result.Files, WrittenFile{
			Path:        file.Path,
			Overwritten: file.Exists && !file.Append,
		})
	}
	return result, nil
}

func writePlannedFile(file PlannedFile, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if file.Append {
		flags |= os.O_APPEND
	} else if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}

	handle, err := os.OpenFile(file.Path, flags, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", file.Path, err)
	}
	if _, err := handle.Write(file.Contents); err != nil {
		_ = handle.Close()
		return fmt.Errorf("write %s: %w", file.Path, err)
	}
	if err := handle.Close(); err != nil {
		return fmt.Errorf("write %s: %w", file.Path, err)
	}
	return nil
}

// Verify loads the emitted workflow through the same full validation path used
// by `jig validate`.
func Verify(result *Result) error {
	if result == nil || result.WorkflowPath == "" {
		return fmt.Errorf("verify scaffold: workflow path is empty")
	}
	if _, err := workflow.Load(result.WorkflowPath); err != nil {
		return fmt.Errorf("verify scaffold workflow %s: %w", result.WorkflowPath, err)
	}
	return nil
}
