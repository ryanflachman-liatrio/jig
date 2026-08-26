package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func WriteSnapshot(path, content string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}
func VerifySnapshot(d Document) error {
	if d.SnapshotPath == "" {
		if Digest(d.Content) != d.SHA256 {
			return fmt.Errorf("document %q digest mismatch", d.ID)
		}
		return nil
	}
	data, err := os.ReadFile(d.SnapshotPath)
	if err != nil {
		return err
	}
	if Digest(string(data)) != d.SHA256 {
		return fmt.Errorf("document %q digest mismatch", d.ID)
	}
	return nil
}
func WriteDraft(path string, draft Draft) error { return writeJSON(path, draft) }
func LoadDraft(path string, draft *Draft) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, draft)
}
func WriteSubmission(path string, sub Submission) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(sub, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if string(existing) != string(data) {
			return fmt.Errorf("submission already exists with different content")
		}
		return nil
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return atomicWrite(path, data)
}
func WriteFeedback(path, content string) error {
	if path == "" {
		return nil
	}
	return atomicWrite(path, []byte(content))
}
func writeJSON(path string, value any) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".review-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
