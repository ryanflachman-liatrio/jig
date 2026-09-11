package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

// templateAssets keeps scaffolds available when jig is run outside its source
// checkout.
//
//go:embed templates
var templateAssets embed.FS

type templateData struct {
	Name string
}

func renderTemplateAsset(assetPath string, data templateData) ([]byte, error) {
	contents, err := templateAssets.ReadFile(assetPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded scaffold asset %q: %w", assetPath, err)
	}

	tmpl, err := template.New(assetPath).Option("missingkey=error").Parse(string(contents))
	if err != nil {
		return nil, fmt.Errorf("parse embedded scaffold asset %q: %w", assetPath, err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render embedded scaffold asset %q: %w", assetPath, err)
	}
	return rendered.Bytes(), nil
}
