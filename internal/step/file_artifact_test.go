package step

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestD05FileArtifactSerialization(t *testing.T) {
	original := Result{Status: StatusSucceeded, OutputPath: "/synthetic/artifacts/binary", OutputFile: FileArtifact{SHA256: strings.Repeat("a", 64), Size: 123, Mode: 0555}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Result
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.OutputFile != original.OutputFile || restored.OutputFile.Validate() != nil {
		t.Fatal(restored)
	}
	raw, err = json.Marshal(Result{Status: StatusSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "output_file") {
		t.Fatalf("host result acquired empty file metadata: %s", raw)
	}
	for _, file := range []FileArtifact{{}, {SHA256: strings.Repeat("a", 64), Size: -1, Mode: 0444}, {SHA256: strings.Repeat("a", 64), Size: 1, Mode: 0644}, {SHA256: strings.Repeat("a", 64), Size: ContainerInputFileBytes + 1, Mode: 0444}} {
		if file.Validate() == nil {
			t.Fatal("accepted invalid metadata", file)
		}
	}
}
