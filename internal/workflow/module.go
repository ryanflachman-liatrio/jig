package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const FieldArtifact FieldType = "artifact"

type moduleExpansion struct {
	sources map[string]ModuleSource
	locked  map[string]ModuleSource
}

type expandedModule struct {
	terminals []string
	exports   map[string]Input
}

// expandModules replaces subworkflow invocations with their namespaced module
// steps. The resulting graph is the only graph the engine validates or runs.
func expandModules(wf *Workflow, baseDir, sourcePath string, locked map[string]ModuleSource) error {
	wf.publicSteps = cloneSteps(wf.Steps)
	e := &moduleExpansion{sources: make(map[string]ModuleSource), locked: locked}
	if err := e.expand(wf, baseDir, sourcePath, nil); err != nil {
		return err
	}
	paths := make([]string, 0, len(e.sources))
	for path := range e.sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	wf.moduleSources = make([]ModuleSource, 0, len(paths))
	for _, path := range paths {
		wf.moduleSources = append(wf.moduleSources, e.sources[path])
	}
	return nil
}

func (e *moduleExpansion) expand(wf *Workflow, baseDir, sourcePath string, stack []string) error {
	if wf.Module != nil {
		if err := markModuleInputs(wf); err != nil {
			return err
		}
	}
	var out []Step
	modules := make(map[string]expandedModule)
	for _, raw := range wf.Steps {
		if raw.Type != StepSubworkflow {
			out = append(out, cloneStep(raw))
			continue
		}
		if err := validateSubworkflowStep(&raw); err != nil {
			return fmt.Errorf("step %q: %w", raw.ID, err)
		}
		if baseDir == "" {
			return fmt.Errorf("step %q: module loading requires a workflow directory", raw.ID)
		}
		modulePath := filepath.Clean(filepath.Join(baseDir, raw.Module))
		if filepath.IsAbs(raw.Module) || modulePath == baseDir || !withinDir(baseDir, modulePath) {
			return fmt.Errorf("step %q: module %q must be a relative path within the workflow directory", raw.ID, raw.Module)
		}
		for _, seen := range stack {
			if seen == modulePath {
				return fmt.Errorf("module cycle: %s -> %s", strings.Join(append(stack, modulePath), " -> "), modulePath)
			}
		}
		absModulePath, err := filepath.Abs(modulePath)
		if err != nil {
			return fmt.Errorf("step %q: resolve module %q: %w", raw.ID, raw.Module, err)
		}
		absModulePath = filepath.Clean(absModulePath)
		data, err := e.moduleData(absModulePath)
		if err != nil {
			return fmt.Errorf("step %q: read module %q: %w", raw.ID, raw.Module, err)
		}
		child, err := decodePrepared(string(data), filepath.Dir(modulePath))
		if err != nil {
			return fmt.Errorf("step %q: module %q: %w", raw.ID, raw.Module, err)
		}
		if child.Module == nil {
			return fmt.Errorf("step %q: module %q is missing [module]", raw.ID, raw.Module)
		}
		if err := validateModuleDeclaration(child); err != nil {
			return fmt.Errorf("step %q: module %q: %w", raw.ID, raw.Module, err)
		}
		rebaseModuleAssets(child, filepath.Dir(absModulePath))
		sum := sha256.Sum256(data)
		e.sources[absModulePath] = ModuleSource{Path: absModulePath, SHA256: hex.EncodeToString(sum[:]), TOML: string(data)}
		if err := e.expand(child, filepath.Dir(absModulePath), absModulePath, append(stack, absModulePath)); err != nil {
			return err
		}
		if err := resolveModuleExports(child, modules); err != nil {
			return fmt.Errorf("step %q: module %q: %w", raw.ID, raw.Module, err)
		}
		if err := validateModuleExports(child); err != nil {
			return fmt.Errorf("step %q: module %q: %w", raw.ID, raw.Module, err)
		}
		inst, err := instantiateModule(child, raw, wf, modules)
		if err != nil {
			return err
		}
		modules[raw.ID] = inst
		steps, err := instSteps(child, raw)
		if err != nil {
			return fmt.Errorf("step %q: %w", raw.ID, err)
		}
		out = append(out, steps...)
	}

	// A module invocation is a public boundary. Its consumer waits for every
	// internal terminal, while an exported value is rewired to its true producer.
	for i := range out {
		st := &out[i]
		var err error
		if st.When, err = rewriteModuleCondition(st.When, modules); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
		if st.AppliesWhen, err = rewriteModuleCondition(st.AppliesWhen, modules); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
		if st.BlockOn, err = rewriteModuleCondition(st.BlockOn, modules); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
		if condition, err := ParseCondition(st.When); err == nil {
			st.DependsOn = uniqueStrings(append(st.DependsOn, condition.Step))
		}
		if condition, err := ParseCondition(st.AppliesWhen); err == nil {
			st.DependsOn = uniqueStrings(append(st.DependsOn, condition.Step))
		}
		for j := range st.Routes {
			if st.Routes[j].When, err = rewriteModuleCondition(st.Routes[j].When, modules); err != nil {
				return fmt.Errorf("step %q route %d: %w", st.ID, j+1, err)
			}
			st.Routes[j].Feedback, err = rewriteModuleReference(st.Routes[j].Feedback, modules)
			if err != nil {
				return fmt.Errorf("step %q route %d: %w", st.ID, j+1, err)
			}
		}
		for j := range st.Review {
			st.Review[j].Source, err = rewriteModuleReference(st.Review[j].Source, modules)
			if err != nil {
				return fmt.Errorf("step %q review %d: %w", st.ID, j+1, err)
			}
			st.Review[j].File, err = rewriteModuleReference(st.Review[j].File, modules)
			if err != nil {
				return fmt.Errorf("step %q review %d: %w", st.ID, j+1, err)
			}
			if ref, fields := parseRef(strings.TrimPrefix(st.Review[j].Reference(), "@")); strings.HasPrefix(st.Review[j].Reference(), "@") && ref != "" && ref != "module" && len(fields) > 0 {
				st.DependsOn = uniqueStrings(append(st.DependsOn, ref))
			}
		}
		var deps []string
		for _, dep := range st.DependsOn {
			if mod, ok := modules[dep]; ok {
				deps = append(deps, mod.terminals...)
			} else {
				deps = append(deps, dep)
			}
		}
		st.DependsOn = uniqueStrings(deps)
		for j := range st.Inputs {
			in := &st.Inputs[j]
			if in.moduleInput != "" && wf.Module == nil {
				return fmt.Errorf("step %q: unresolved module input %q", st.ID, in.moduleInput)
			}
			if mod, ok := modules[in.Ref]; ok {
				if len(in.RefField) != 1 || in.Artifact != "" {
					return fmt.Errorf("step %q: module input @%s must name one export", st.ID, in.Ref)
				}
				export, ok := mod.exports[in.RefField[0]]
				if !ok {
					return fmt.Errorf("step %q: module %q has no export %q", st.ID, in.Ref, in.RefField[0])
				}
				*in = export
			}
			if in.Ref != "" {
				st.DependsOn = uniqueStrings(append(st.DependsOn, in.Ref))
			}
		}
	}
	wf.Steps = out
	wf.applyDefaults()
	if err := resolveModuleExports(wf, modules); err != nil {
		return err
	}
	return nil
}

// rewriteModuleCondition resolves a public module export in a guard to the
// internal producer that owns the exported value. Conditions have to be
// rewritten alongside inputs; otherwise a parent could consume an export but
// not use it to express a phase transition.
func rewriteModuleCondition(raw string, modules map[string]expandedModule) (string, error) {
	if raw == "" {
		return "", nil
	}
	condition, err := ParseCondition(raw)
	if err != nil {
		return raw, nil
	}
	module, ok := modules[condition.Step]
	if !ok {
		return raw, nil
	}
	if len(condition.Field) == 0 {
		return "", fmt.Errorf("module %q conditions must name an export", condition.Step)
	}
	export, ok := module.exports[condition.Field[0]]
	if !ok {
		return "", fmt.Errorf("module %q has no export %q", condition.Step, condition.Field[0])
	}
	if export.Artifact != "" {
		return "", fmt.Errorf("module export %q is an artifact and cannot be used in a condition", condition.Field[0])
	}
	left := export.Ref
	if len(export.RefField) > 0 {
		left += "." + strings.Join(export.RefField, ".")
	}
	switch condition.Op {
	case CondTruthy:
		return left, nil
	case CondEq, CondNeq:
		return left + " " + string(condition.Op) + " " + fmt.Sprintf("%q", condition.Value), nil
	default:
		return "", fmt.Errorf("unsupported condition operator %q", condition.Op)
	}
}

func rewriteModuleReference(raw string, modules map[string]expandedModule) (string, error) {
	if !strings.HasPrefix(raw, "@") {
		return raw, nil
	}
	step, fields := parseRef(strings.TrimPrefix(raw, "@"))
	module, ok := modules[step]
	if !ok {
		return raw, nil
	}
	if len(fields) != 1 {
		return "", fmt.Errorf("module reference @%s must name one export", step)
	}
	export, ok := module.exports[fields[0]]
	if !ok {
		return "", fmt.Errorf("module %q has no export %q", step, fields[0])
	}
	if export.Artifact != "" {
		return "@" + export.Ref + "." + export.Artifact, nil
	}
	result := "@" + export.Ref
	if len(export.RefField) > 0 {
		result += "." + strings.Join(export.RefField, ".")
	}
	return result, nil
}

func (e *moduleExpansion) moduleData(path string) ([]byte, error) {
	if e.locked != nil {
		source, ok := e.locked[path]
		if !ok {
			return nil, fmt.Errorf("locked module source is missing")
		}
		data := []byte(source.TOML)
		sum := sha256.Sum256(data)
		if source.SHA256 != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("locked module checksum mismatch")
		}
		return data, nil
	}
	return os.ReadFile(path)
}

func markModuleInputs(wf *Workflow) error {
	for i := range wf.Steps {
		for j := range wf.Steps[i].Inputs {
			in := &wf.Steps[i].Inputs[j]
			if in.Ref != "module" {
				continue
			}
			if len(in.RefField) != 1 || in.Artifact != "" {
				return fmt.Errorf("module step %q: input @module must name one declared module input", wf.Steps[i].ID)
			}
			if _, ok := wf.Module.Inputs[in.RefField[0]]; !ok {
				return fmt.Errorf("module step %q: unknown module input %q", wf.Steps[i].ID, in.RefField[0])
			}
			in.moduleInput = in.RefField[0]
			in.Ref, in.RefField = "", nil
		}
		for j := range wf.Steps[i].Review {
			target := &wf.Steps[i].Review[j]
			for _, ref := range []string{target.Source, target.File} {
				stepID, fields := parseRef(strings.TrimPrefix(ref, "@"))
				if !strings.HasPrefix(ref, "@") || stepID != "module" {
					continue
				}
				if len(fields) != 1 {
					return fmt.Errorf("module step %q: review @module reference must name one declared module input", wf.Steps[i].ID)
				}
				if _, ok := wf.Module.Inputs[fields[0]]; !ok {
					return fmt.Errorf("module step %q: unknown module input %q", wf.Steps[i].ID, fields[0])
				}
				target.moduleInput = fields[0]
			}
			if target.moduleInput != "" && target.Source != "" && target.File != "" {
				return fmt.Errorf("module step %q: review target sets both source and file", wf.Steps[i].ID)
			}
		}
	}
	return nil
}

func instSteps(child *Workflow, parent Step) ([]Step, error) {
	steps := cloneSteps(child.Steps)
	internal := make(map[string]bool, len(child.Steps))
	for _, step := range child.Steps {
		internal[step.ID] = true
	}
	for i := range steps {
		isRoot := true
		for _, dep := range steps[i].DependsOn {
			if internal[dep] {
				isRoot = false
				break
			}
		}
		if isRoot {
			steps[i].DependsOn = uniqueStrings(append(steps[i].DependsOn, parent.DependsOn...))
			if parent.When != "" {
				if steps[i].When != "" {
					return nil, fmt.Errorf("module root %q already has a when guard; cannot combine it with the invocation guard", steps[i].ID)
				}
				steps[i].When = parent.When
			}
		}
		prefixStep(&steps[i], parent.ID, internal)
	}
	return steps, nil
}

func instantiateModule(child *Workflow, parent Step, parentWF *Workflow, siblings map[string]expandedModule) (expandedModule, error) {
	bindings := make(map[string]Input, len(child.Module.Inputs))
	for name, spec := range child.Module.Inputs {
		raw, ok := parent.With[name]
		if !ok {
			if spec.Required == nil || *spec.Required {
				return expandedModule{}, fmt.Errorf("step %q: module input %q is required", parent.ID, name)
			}
			continue
		}
		binding, err := parseModuleBinding(raw, spec)
		if err != nil {
			return expandedModule{}, fmt.Errorf("step %q: binding %q: %w", parent.ID, name, err)
		}
		if binding.moduleInput != "" {
			parentInput, ok := parentWF.Module.Inputs[binding.moduleInput]
			if !ok || !sameModuleValue(parentInput, spec) {
				return expandedModule{}, fmt.Errorf("step %q: binding %q: module input %q has a different type", parent.ID, name, binding.moduleInput)
			}
		} else if sibling, ok := siblings[binding.Ref]; ok {
			if len(binding.RefField) != 1 || binding.Artifact != "" {
				return expandedModule{}, fmt.Errorf("step %q: binding %q must name one export from module %q", parent.ID, name, binding.Ref)
			}
			export, ok := sibling.exports[binding.RefField[0]]
			if !ok {
				return expandedModule{}, fmt.Errorf("step %q: binding %q references unknown export %q", parent.ID, name, binding.RefField[0])
			}
			binding = export
		} else if err := validateBindingType(parentWF, binding, spec); err != nil {
			return expandedModule{}, fmt.Errorf("step %q: binding %q: %w", parent.ID, name, err)
		}
		bindings[name] = binding
	}
	for name := range parent.With {
		if _, ok := child.Module.Inputs[name]; !ok {
			return expandedModule{}, fmt.Errorf("step %q: unknown module input binding %q", parent.ID, name)
		}
	}

	for i := range child.Steps {
		for j := range child.Steps[i].Inputs {
			in := &child.Steps[i].Inputs[j]
			if in.moduleInput == "" {
				continue
			}
			binding, ok := bindings[in.moduleInput]
			if !ok {
				return expandedModule{}, fmt.Errorf("module step %q: unresolved input %q", child.Steps[i].ID, in.moduleInput)
			}
			*in = binding
		}
		for j := range child.Steps[i].Review {
			target := &child.Steps[i].Review[j]
			if target.moduleInput == "" {
				continue
			}
			binding, ok := bindings[target.moduleInput]
			if !ok {
				return expandedModule{}, fmt.Errorf("module step %q: unresolved review input %q", child.Steps[i].ID, target.moduleInput)
			}
			if binding.moduleInput != "" {
				target.moduleInput = binding.moduleInput
				if target.File != "" {
					target.File = "@module." + target.moduleInput
				} else {
					target.Source = "@module." + target.moduleInput
				}
				continue
			}
			if binding.Path != "" || binding.Artifact != "" || binding.Ref == "" || len(binding.RefField) == 0 {
				return expandedModule{}, fmt.Errorf("module step %q: review @module.%s must bind an upstream text field", child.Steps[i].ID, target.moduleInput)
			}
			ref := "@" + binding.Ref + "." + strings.Join(binding.RefField, ".")
			if target.File != "" {
				target.File = ref
			} else {
				target.Source = ref
			}
			target.moduleInput = ""
		}
	}

	used := make(map[string]bool)
	for _, st := range child.Steps {
		for _, dep := range st.DependsOn {
			used[dep] = true
		}
	}
	var terminals []string
	for _, st := range child.Steps {
		if !used[st.ID] {
			terminals = append(terminals, parent.ID+"__"+st.ID)
		}
	}
	exports := make(map[string]Input, len(child.Module.Exports))
	for name, export := range child.Module.Exports {
		in, err := exportInput(export)
		if err != nil {
			return expandedModule{}, fmt.Errorf("module export %q: %w", name, err)
		}
		in.Ref = parent.ID + "__" + in.Ref
		exports[name] = in
	}
	return expandedModule{terminals: terminals, exports: exports}, nil
}

func validateModuleDeclaration(wf *Workflow) error {
	m := wf.Module
	if m.SchemaVersion != ModuleSchemaVersion {
		return fmt.Errorf("module.schema_version = %d, want %d", m.SchemaVersion, ModuleSchemaVersion)
	}
	if len(m.Exports) == 0 {
		return fmt.Errorf("[module.exports] declares no exports")
	}
	for name, value := range m.Inputs {
		if !isIdent(name) {
			return fmt.Errorf("module input %q is not a valid identifier", name)
		}
		if !validModuleValue(value) {
			return fmt.Errorf("module input %q has invalid type %q", name, value.Type)
		}
	}
	for name, export := range m.Exports {
		if !isIdent(name) {
			return fmt.Errorf("module export %q is not a valid identifier", name)
		}
		if _, err := exportInput(export); err != nil {
			return fmt.Errorf("module export %q: %w", name, err)
		}
	}
	return nil
}

func validateModuleExports(wf *Workflow) error {
	for name, export := range wf.Module.Exports {
		in, err := exportInput(export)
		if err != nil {
			return fmt.Errorf("module export %q: %w", name, err)
		}
		idx, ok := wf.index[in.Ref]
		if !ok {
			return fmt.Errorf("module export %q references unknown internal step %q", name, in.Ref)
		}
		producer := &wf.Steps[idx]
		if in.Artifact != "" {
			if producer.Type != StepCheck || producer.Findings == nil || producer.Findings.Artifacts[in.Artifact] == "" {
				return fmt.Errorf("module export %q references undeclared check artifact", name)
			}
			continue
		}
		if len(in.RefField) == 0 {
			continue
		}
		if _, ok := fieldForStep(producer, in.RefField); !ok {
			return fmt.Errorf("module export %q references unknown field %q", name, strings.Join(in.RefField, "."))
		}
	}
	return nil
}

func resolveModuleExports(wf *Workflow, modules map[string]expandedModule) error {
	if wf.Module == nil {
		return nil
	}
	for name, export := range wf.Module.Exports {
		in, err := exportInput(export)
		if err != nil {
			return fmt.Errorf("module export %q: %w", name, err)
		}
		module, ok := modules[in.Ref]
		if !ok {
			continue
		}
		if len(in.RefField) != 1 || in.Artifact != "" {
			return fmt.Errorf("module export %q must name one export from nested module %q", name, in.Ref)
		}
		resolved, ok := module.exports[in.RefField[0]]
		if !ok {
			return fmt.Errorf("module export %q references unknown nested export %q", name, in.RefField[0])
		}
		wf.Module.Exports[name] = moduleExportFromInput(resolved)
	}
	return nil
}

func moduleExportFromInput(in Input) ModuleExport {
	if in.Artifact != "" {
		return ModuleExport{Artifact: "@" + in.Ref + "." + in.Artifact}
	}
	ref := "@" + in.Ref
	if len(in.RefField) > 0 {
		ref += "." + strings.Join(in.RefField, ".")
	}
	return ModuleExport{Ref: ref}
}

func validModuleValue(v ModuleValue) bool {
	switch v.Type {
	case FieldText, FieldNumber, FieldBool, FieldEnum, FieldArtifact:
		return v.Type != FieldEnum || len(v.Enum) > 0
	}
	return false
}

func sameModuleValue(a, b ModuleValue) bool {
	return a.Type == b.Type && (a.Type != FieldEnum || sameStringSet(a.Enum, b.Enum))
}

func validateSubworkflowStep(s *Step) error {
	if s.Module == "" {
		return fmt.Errorf("subworkflow requires `module`")
	}
	if s.Run != "" || s.Script != "" || s.Skill != "" || s.AgentFile != "" || len(s.Review) > 0 || len(s.Inputs) > 0 || s.Findings != nil {
		return fmt.Errorf("subworkflow sets fields belonging to an executable step")
	}
	return nil
}

func parseModuleBinding(raw string, want ModuleValue) (Input, error) {
	if raw == "" {
		return Input{}, fmt.Errorf("binding is blank")
	}
	if !strings.HasPrefix(raw, "@") {
		if want.Type != FieldText {
			return Input{}, fmt.Errorf("literal paths only satisfy text inputs")
		}
		return Input{Path: raw}, nil
	}
	stepID, fields := parseRef(strings.TrimPrefix(raw, "@"))
	if stepID == "module" {
		if len(fields) != 1 {
			return Input{}, fmt.Errorf("module binding %q must name one input", raw)
		}
		return Input{moduleInput: fields[0]}, nil
	}
	if want.Type == FieldArtifact {
		if len(fields) != 1 {
			return Input{}, fmt.Errorf("artifact binding %q must be @step.export", raw)
		}
		return Input{Ref: stepID, Artifact: fields[0]}, nil
	}
	return Input{Ref: stepID, RefField: fields}, nil
}

func validateBindingType(wf *Workflow, in Input, want ModuleValue) error {
	if in.Path != "" {
		return nil
	}
	if in.Ref == "" {
		return nil
	}
	idx, ok := wf.index[in.Ref]
	if !ok {
		// It may be a sibling subworkflow export; expansion rewrites it later.
		return nil
	}
	producer := &wf.Steps[idx]
	if want.Type == FieldArtifact {
		if producer.Type != StepCheck || producer.Findings == nil || producer.Findings.Artifacts[in.Artifact] == "" {
			return fmt.Errorf("expects an artifact but %s is not a declared check artifact", in.String())
		}
		return nil
	}
	if len(in.RefField) == 0 {
		if want.Type != FieldText || producer.OutputType.Kind != OutputText {
			return fmt.Errorf("expects %s but %s is an untyped output", want.Type, in.String())
		}
		return nil
	}
	f, ok := fieldForStep(producer, in.RefField)
	if !ok || f.Type != want.Type || (want.Type == FieldEnum && !sameStringSet(f.Enum, want.Enum)) {
		return fmt.Errorf("expects %s but %s has a different type", want.Type, in.String())
	}
	return nil
}

func exportInput(export ModuleExport) (Input, error) {
	if (export.Ref == "") == (export.Artifact == "") {
		return Input{}, fmt.Errorf("set exactly one of ref or artifact")
	}
	if export.Artifact != "" {
		stepID, fields := parseRef(strings.TrimPrefix(export.Artifact, "@"))
		if len(fields) != 1 {
			return Input{}, fmt.Errorf("artifact %q must be @step.export", export.Artifact)
		}
		return Input{Ref: stepID, Artifact: fields[0]}, nil
	}
	stepID, fields := parseRef(strings.TrimPrefix(export.Ref, "@"))
	if stepID == "" {
		return Input{}, fmt.Errorf("ref %q is invalid", export.Ref)
	}
	return Input{Ref: stepID, RefField: fields}, nil
}

func prefixStep(s *Step, prefix string, internal map[string]bool) {
	oldID := s.ID
	s.ID = prefix + "__" + oldID
	for i, dep := range s.DependsOn {
		if internal[dep] {
			s.DependsOn[i] = prefix + "__" + dep
		}
	}
	for i := range s.Inputs {
		if internal[s.Inputs[i].Ref] {
			s.Inputs[i].Ref = prefix + "__" + s.Inputs[i].Ref
		}
	}
	s.When = prefixCondition(s.When, prefix, internal)
	s.AppliesWhen = prefixCondition(s.AppliesWhen, prefix, internal)
	s.BlockOn = prefixCondition(s.BlockOn, prefix, internal)
	for i := range s.Routes {
		if internal[s.Routes[i].Goto] {
			s.Routes[i].Goto = prefix + "__" + s.Routes[i].Goto
		}
		s.Routes[i].When = prefixCondition(s.Routes[i].When, prefix, internal)
		s.Routes[i].Feedback = prefixFeedback(s.Routes[i].Feedback, prefix, internal)
	}
	for i := range s.Review {
		s.Review[i].Source = prefixFeedback(s.Review[i].Source, prefix, internal)
		s.Review[i].File = prefixFeedback(s.Review[i].File, prefix, internal)
	}
}

func prefixCondition(raw, prefix string, internal map[string]bool) string {
	if raw == "" {
		return ""
	}
	condition, err := ParseCondition(raw)
	if err != nil || !internal[condition.Step] {
		return raw
	}
	left := prefix + "__" + condition.Step
	if len(condition.Field) > 0 {
		left += "." + strings.Join(condition.Field, ".")
	}
	switch condition.Op {
	case CondTruthy:
		return left
	case CondEq:
		return left + " == " + fmt.Sprintf("%q", condition.Value)
	default:
		return left + " != " + fmt.Sprintf("%q", condition.Value)
	}
}

func prefixFeedback(raw, prefix string, internal map[string]bool) string {
	if !strings.HasPrefix(raw, "@") {
		return raw
	}
	id, fields := parseRef(strings.TrimPrefix(raw, "@"))
	if !internal[id] {
		return raw
	}
	result := "@" + prefix + "__" + id
	if len(fields) > 0 {
		result += "." + strings.Join(fields, ".")
	}
	return result
}

func rebaseModuleAssets(wf *Workflow, baseDir string) {
	for i := range wf.Steps {
		step := &wf.Steps[i]
		if step.Skill != "" && !filepath.IsAbs(step.Skill) {
			step.Skill = filepath.Join(baseDir, step.Skill)
		}
		if step.AgentFile != "" && !filepath.IsAbs(step.AgentFile) {
			step.AgentFile = filepath.Join(baseDir, step.AgentFile)
		}
		if step.OutputTemplate != "" && !filepath.IsAbs(step.OutputTemplate) {
			step.OutputTemplate = filepath.Join(baseDir, step.OutputTemplate)
		}
		if step.SchemaFile != "" && !filepath.IsAbs(step.SchemaFile) {
			step.SchemaFile = filepath.Join(baseDir, step.SchemaFile)
		}
	}
}

func fieldForStep(s *Step, path []string) (*Field, bool) {
	if len(path) == 0 {
		return nil, false
	}
	if s.Type == StepAgent {
		return MergedSchema(s.Schema).lookup(path)
	}
	return s.Schema.lookup(path)
}

func cloneSteps(in []Step) []Step {
	out := make([]Step, len(in))
	for i := range in {
		out[i] = cloneStep(in[i])
	}
	return out
}

func cloneStep(in Step) Step {
	out := in
	out.DependsOn = append([]string(nil), in.DependsOn...)
	out.Inputs = append([]Input(nil), in.Inputs...)
	out.Routes = append([]Route(nil), in.Routes...)
	out.Review = append([]ReviewTarget(nil), in.Review...)
	if in.With != nil {
		out.With = make(map[string]string, len(in.With))
		for key, value := range in.With {
			out.With[key] = value
		}
	}
	return out
}

func withinDir(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, value := range a {
		seen[value] = true
	}
	if len(seen) != len(a) {
		return false
	}
	for _, value := range b {
		if !seen[value] {
			return false
		}
	}
	return true
}
