//go:build darwin || linux

package workflow

// P3 qualification cases now exercise the production collector.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// P3 policy probes exercise the production collector.
const p3FileLimit = captureFileLimit

type p3Error = captureError

const p3Limit = captureLimit
const p3Forbidden = captureForbidden
const p3Unsupported = captureUnsupported
const p3Integrity = captureIntegrity
const p3Storage = captureStorage

type p3Ledger = captureLedger
type p3Capture = captureCapture

var p3ValidatePath = captureValidatePath
var p3Pin = capturePin
var p3SameSource = captureSameSource

type p3Namespace = captureNamespace

var p3SelectBundle = captureSelectBundle

func p3Write(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func p3Root(t *testing.T) (string, *os.File) {
	t.Helper()
	dir := t.TempDir()
	root, err := p3Pin(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return dir, root
}

func TestAuthoringAssetPolicy(t *testing.T) {
	t.Run("file_boundary_and_empty", func(t *testing.T) {
		dir, root := p3Root(t)
		for _, size := range []int{0, p3FileLimit, p3FileLimit + 1} {
			p3Write(t, dir, "source", bytes.Repeat([]byte("x"), size))
			ledger := new(p3Ledger)
			got, err := ledger.capture(root, "source", nil)
			if size > p3FileLimit {
				if !errors.Is(err, p3Limit) || ledger.read != p3FileLimit+1 {
					t.Fatalf("err=%v read=%d", err, ledger.read)
				}
			} else if err != nil || len(got.bytes) != size {
				t.Fatalf("size=%d captured=%d err=%v", size, len(got.bytes), err)
			}
		}
	})
	t.Run("dedup_keeps_bindings_and_charges_reads", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, "source", []byte("abc"))
		ledger := new(p3Ledger)
		for i := 0; i < 2048; i++ {
			got, err := ledger.capture(root, "source", nil)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprintf("%x", got.digest) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
				t.Fatal("unexpected SHA-256")
			}
			got.bytes[0] = 'z'
		}
		_, err := ledger.capture(root, "source", nil)
		if !errors.Is(err, p3Limit) || ledger.read != 3*2048 || ledger.unique != 3 || len(ledger.blobs) != 1 {
			t.Fatalf("ledger=%+v err=%v", ledger, err)
		}
		for _, b := range ledger.blobs {
			if string(b) != "abc" {
				t.Fatal("mutable returned bytes alias authority")
			}
		}
	})
	t.Run("unique_byte_boundary", func(t *testing.T) {
		dir, root := p3Root(t)
		ledger := new(p3Ledger)
		for i := 0; i < 16; i++ {
			p3Write(t, dir, "source", bytes.Repeat([]byte{byte(i)}, p3FileLimit))
			if _, err := ledger.capture(root, "source", nil); err != nil {
				t.Fatal(err)
			}
		}
		p3Write(t, dir, "source", []byte("x"))
		_, err := ledger.capture(root, "source", nil)
		if !errors.Is(err, p3Limit) || ledger.unique != 16<<20 {
			t.Fatalf("unique=%d err=%v", ledger.unique, err)
		}
	})
	t.Run("cumulative_reads_despite_dedup", func(t *testing.T) {
		dir, root := p3Root(t)
		ledger := new(p3Ledger)
		p3Write(t, dir, "source", bytes.Repeat([]byte("x"), p3FileLimit))
		for i := 0; i < 32; i++ {
			if _, err := ledger.capture(root, "source", nil); err != nil {
				t.Fatal(err)
			}
		}
		_, err := ledger.capture(root, "source", nil)
		if !errors.Is(err, p3Limit) || ledger.read != (32<<20)+1 {
			t.Fatalf("read=%d err=%v", ledger.read, err)
		}
	})
	t.Run("blob_boundary", func(t *testing.T) {
		dir, root := p3Root(t)
		ledger := new(p3Ledger)
		for i := 0; i <= 1024; i++ {
			p3Write(t, dir, "source", []byte(fmt.Sprint(i)))
			_, err := ledger.capture(root, "source", nil)
			if i < 1024 && err != nil {
				t.Fatal(err)
			}
			if i == 1024 && !errors.Is(err, p3Limit) {
				t.Fatalf("err=%v", err)
			}
		}
	})
	t.Run("forbidden_paths_before_read", func(t *testing.T) {
		dir, root := p3Root(t)
		for _, name := range []string{".git/config", ".JIG/blob", ".ssh/key", ".aws/key", ".azure/key", ".kube/key", ".gnupg/key", ".docker/key", ".env", ".env.example", ".netrc", ".npmrc", ".pypirc", "auth.json", "credentials.json", ".config/gcloud/file", "bad\nname", "bad\x00name"} {
			ledger := new(p3Ledger)
			_, err := ledger.capture(root, name, nil)
			if !errors.Is(err, p3Forbidden) || ledger.read != 0 {
				t.Fatalf("path=%q err=%v bytes=%d", name, err, ledger.read)
			}
		}
		// A declared alias of an excluded anchor must also be rejected.
		if err := os.Mkdir(filepath.Join(dir, ".jig"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(".jig", filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
		if f, err := p3Pin(filepath.Join(dir, "alias")); !errors.Is(err, p3Forbidden) {
			if f != nil {
				f.Close()
			}
			t.Fatalf("alias err=%v", err)
		}
	})
	t.Run("file_links_and_directory_links", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, "target", []byte("ORIGINAL"))
		p3Write(t, dir, "sub/file", []byte("nested"))
		for _, tc := range []struct {
			name, target string
			allowed      bool
		}{
			{"relative", "target", true}, {"absolute", filepath.Join(dir, "target"), false}, {"escape", "../outside", false}, {"excluded", ".env", false}, {"directory", "sub", false},
		} {
			if err := os.Symlink(tc.target, filepath.Join(dir, tc.name)); err != nil {
				t.Fatal(err)
			}
			got, err := new(p3Ledger).capture(root, tc.name, nil)
			if tc.allowed {
				if err != nil || string(got.bytes) != "ORIGINAL" {
					t.Fatalf("%s: %q %v", tc.name, got.bytes, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		}
		if _, err := new(p3Ledger).capture(root, "directory/file", nil); err == nil {
			t.Fatal("followed directory symlink")
		}
	})
	t.Run("symlink_resolution_boundaries", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, "link16", []byte("original"))
		for i := 15; i >= 0; i-- {
			if err := os.Symlink(fmt.Sprintf("link%d", i+1), filepath.Join(dir, fmt.Sprintf("link%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		ledger := new(p3Ledger)
		for i := 0; i < 64; i++ {
			if _, err := ledger.capture(root, "link0", nil); err != nil {
				t.Fatal(err)
			}
		}
		if ledger.links != 1024 {
			t.Fatalf("links=%d", ledger.links)
		}
		if _, err := ledger.capture(root, "link0", nil); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
		if err := os.Symlink("link0", filepath.Join(dir, "seventeenth")); err != nil {
			t.Fatal(err)
		}
		if _, err := new(p3Ledger).capture(root, "seventeenth", nil); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
	t.Run("fifo_and_directory_do_not_block", func(t *testing.T) {
		dir, root := p3Root(t)
		if err := unix.Mkfifo(filepath.Join(dir, "fifo"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"fifo", "."} {
			if _, err := new(p3Ledger).capture(root, name, nil); !errors.Is(err, p3Unsupported) {
				t.Fatalf("%s: %v", name, err)
			}
		}
	})
	t.Run("replacement_after_open_rejected", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, "source", []byte("ORIGINAL"))
		got, err := new(p3Ledger).capture(root, "source", func() {
			if err := os.Rename(filepath.Join(dir, "source"), filepath.Join(dir, "old")); err != nil {
				t.Fatal(err)
			}
			p3Write(t, dir, "source", []byte("REPLACEMENT"))
		})
		if !errors.Is(err, p3Integrity) || len(got.bytes) != 0 {
			t.Fatalf("accepted replaced source: %q %v", got.bytes, err)
		}
	})
	t.Run("path_and_namespace_rejection", func(t *testing.T) {
		for _, path := range []string{strings.Repeat("a", 256), strings.Repeat("a/", 2048) + "z", string([]byte{0xff})} {
			if p3ValidatePath(path) == nil {
				t.Fatal("accepted invalid or over-limit path")
			}
		}
		names := make(p3Namespace)
		for _, name := range []string{"README", "readme", "e\u0301", "é"} {
			err := names.add(name, false)
			if name == "readme" || name == "e\u0301" {
				if !errors.Is(err, p3Forbidden) {
					t.Fatalf("accepted ambiguous name: %q", name)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
		for _, paths := range [][]string{{"file", "file/child"}, {"file/child", "file"}} {
			names := make(p3Namespace)
			if err := names.add(paths[0], false); err != nil {
				t.Fatal(err)
			}
			if err := names.add(paths[1], false); !errors.Is(err, p3Forbidden) {
				t.Fatal("accepted file prefix collision")
			}
		}
	})
}

func TestAuthoringAssetPolicyBundle(t *testing.T) {
	t.Run("file_count", func(t *testing.T) {
		dir, root := p3Root(t)
		for i := 0; i < 256; i++ {
			p3Write(t, dir, fmt.Sprintf("file%03d", i), nil)
		}
		files, err := new(p3Ledger).bundle(root)
		if err != nil || len(files) != 256 {
			t.Fatalf("files=%d err=%v", len(files), err)
		}
		p3Write(t, dir, "one-more", nil)
		if _, err := new(p3Ledger).bundle(root); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
	t.Run("bundle_byte_count", func(t *testing.T) {
		dir, root := p3Root(t)
		for i := 0; i < 4; i++ {
			p3Write(t, dir, fmt.Sprint(i), bytes.Repeat([]byte("x"), 1<<20))
		}
		if _, err := new(p3Ledger).bundle(root); err != nil {
			t.Fatal(err)
		}
		p3Write(t, dir, "one-more", []byte("x"))
		if _, err := new(p3Ledger).bundle(root); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		dir, root := p3Root(t)
		deep := strings.Repeat("a/", 16)
		p3Write(t, dir, deep+"leaf", nil)
		if _, err := new(p3Ledger).bundle(root); err != nil {
			t.Fatal(err)
		}
		p3Write(t, dir, deep+"b/leaf", nil)
		if _, err := new(p3Ledger).bundle(root); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
	t.Run("entries_before_content", func(t *testing.T) {
		dir, root := p3Root(t)
		for i := 0; i < 8192; i++ {
			if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("dir%04d", i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		ledger := new(p3Ledger)
		if _, err := ledger.bundle(root); err != nil || ledger.visited != 8192 {
			t.Fatalf("visited=%d err=%v", ledger.visited, err)
		}
		if err := os.Mkdir(filepath.Join(dir, "one-more"), 0700); err != nil {
			t.Fatal(err)
		}
		ledger = new(p3Ledger)
		if _, err := ledger.bundle(root); !errors.Is(err, p3Limit) || ledger.visited != 8192 {
			t.Fatalf("visited=%d err=%v", ledger.visited, err)
		}
	})
	t.Run("excluded_entry_invalidates_bundle", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, "helper", []byte("allowed"))
		p3Write(t, dir, ".env", []byte("SYNTHETIC_EXCLUDED"))
		files, err := new(p3Ledger).bundle(root)
		if !errors.Is(err, p3Forbidden) || files != nil {
			t.Fatalf("partial bundle accepted: %v", err)
		}
	})
	t.Run("outside_directory_link", func(t *testing.T) {
		dir, root := p3Root(t)
		outside := t.TempDir()
		p3Write(t, outside, "source", []byte("OUTSIDE"))
		if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
		files, err := new(p3Ledger).bundle(root)
		if err == nil || files != nil {
			t.Fatal("accepted outside directory link")
		}
	})
	t.Run("component_count", func(t *testing.T) {
		dir, root := p3Root(t)
		p3Write(t, dir, strings.Repeat("a/", 63)+"f", []byte("allowed"))
		if _, err := new(p3Ledger).capture(root, strings.Repeat("a/", 63)+"f", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := new(p3Ledger).capture(root, strings.Repeat("a/", 64)+"f", nil); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
}

// Probe the pre-expansion boundary using bounded source reads and a compact
// plan. The ordinary loader is called only after this independent preflight.
// It does not replace production module validation or enable capture at Start.
func p3Graph(path string) (int, error) {
	count := 0
	ledger := new(p3Ledger)
	var visit func(string, int) error
	visit = func(path string, depth int) error {
		if depth > 32 {
			return p3Limit
		}
		root, err := p3Pin(filepath.Dir(path))
		if err != nil {
			return err
		}
		source, err := ledger.capture(root, filepath.Base(path), nil)
		closeErr := root.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return p3Storage
		}
		var raw struct {
			Steps []struct {
				Module string `toml:"module"`
			} `toml:"step"`
		}
		if _, err := toml.Decode(string(source.bytes), &raw); err != nil {
			return p3Integrity
		}
		for _, step := range raw.Steps {
			if step.Module != "" {
				child := filepath.Join(filepath.Dir(path), step.Module)
				if filepath.IsAbs(step.Module) || !withinDir(filepath.Dir(path), child) {
					return p3Forbidden
				}
				if err := visit(child, depth+1); err != nil {
					return err
				}
			} else {
				if count >= 1024 {
					return p3Limit
				}
				count++
			}
		}
		return nil
	}
	if err := visit(path, 0); err != nil {
		return count, err
	}
	return count, nil
}

func TestAuthoringAssetPolicyGraph(t *testing.T) {
	t.Run("expanded_steps", func(t *testing.T) {
		dir := t.TempDir()
		p3Write(t, dir, "module.toml", []byte("[module]\nschema_version=1\n[module.exports.result]\nref='@worker'\n[[step]]\nid='worker'\ntype='command'\nrun='true'\n"))
		var source strings.Builder
		source.WriteString("[workflow]\nname='p3'\nversion='1'\n")
		for i := 0; i < 1024; i++ {
			fmt.Fprintf(&source, "[[step]]\nid='call%d'\ntype='subworkflow'\nmodule='module.toml'\n", i)
		}
		p3Write(t, dir, "workflow.toml", []byte(source.String()))
		count, err := p3Graph(filepath.Join(dir, "workflow.toml"))
		if err != nil || count != 1024 {
			t.Fatalf("count=%d err=%v", count, err)
		}
		wf, err := Load(filepath.Join(dir, "workflow.toml"))
		if err != nil || len(wf.Steps) != 1024 {
			t.Fatalf("Load err=%v", err)
		}
		source.WriteString("[[step]]\nid='extra'\ntype='subworkflow'\nmodule='module.toml'\n")
		p3Write(t, dir, "workflow.toml", []byte(source.String()))
		if count, err := p3Graph(filepath.Join(dir, "workflow.toml")); !errors.Is(err, p3Limit) || count != 1024 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	})
	t.Run("import_depth", func(t *testing.T) {
		dir := t.TempDir()
		for depth := 0; depth <= 32; depth++ {
			prefix := "[module]\nschema_version=1\n"
			if depth == 0 {
				prefix = "[workflow]\nname='p3'\nversion='1'\n"
			}
			body := fmt.Sprintf("[[step]]\nid='nested'\ntype='subworkflow'\nmodule='module%d.toml'\n", depth+1)
			if depth == 32 {
				body = "[[step]]\nid='worker'\ntype='command'\nrun='true'\n"
			}
			p3Write(t, dir, fmt.Sprintf("module%d.toml", depth), []byte(prefix+body))
		}
		if count, err := p3Graph(filepath.Join(dir, "module0.toml")); err != nil || count != 1 {
			t.Fatalf("count=%d err=%v", count, err)
		}
		p3Write(t, dir, "module32.toml", []byte("[module]\nschema_version=1\n[[step]]\nid='extra'\ntype='subworkflow'\nmodule='module33.toml'\n"))
		if _, err := p3Graph(filepath.Join(dir, "module0.toml")); !errors.Is(err, p3Limit) {
			t.Fatal(err)
		}
	})
}

func p3Metadata(r io.Reader, limit int64) (json.RawMessage, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, p3Storage
	}
	if int64(len(data)) > limit {
		return nil, p3Limit
	}
	if !json.Valid(data) {
		return nil, p3Integrity
	}
	return json.RawMessage(data), nil
}

func TestAuthoringAssetPolicyMetadataAndNames(t *testing.T) {
	for _, limit := range []int{8 << 20, 32 << 20} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			data := append([]byte("{}"), bytes.Repeat([]byte(" "), limit-2)...)
			if _, err := p3Metadata(bytes.NewReader(data), int64(limit)); err != nil {
				t.Fatal(err)
			}
			data = append(data, ' ')
			if _, err := p3Metadata(bytes.NewReader(data), int64(limit)); !errors.Is(err, p3Limit) {
				t.Fatal(err)
			}
		})
	}
	// Policy accepts the exact encoded-name bounds; the OS may have a smaller
	// path limit. These are upper bounds, not a promise that every name exists.
	for _, size := range []int{255, 256} {
		err := p3ValidatePath(strings.Repeat("a", size))
		if size == 255 && err != nil || size == 256 && !errors.Is(err, p3Limit) {
			t.Fatalf("component=%d err=%v", size, err)
		}
	}
	path := strings.Repeat(strings.Repeat("a", 255)+"/", 16)
	if len(path) != 4096 {
		t.Fatal("bad fixture")
	}
	if err := p3ValidatePath(path); err != nil {
		t.Fatal(err)
	}
	if err := p3ValidatePath(path + "a"); !errors.Is(err, p3Limit) {
		t.Fatal(err)
	}
	for _, name := range []string{"/dev/file", "/proc/file", "/sys/file"} {
		if _, err := p3Pin(name); !errors.Is(err, p3Forbidden) {
			t.Fatal(err)
		}
	}
}

func TestAuthoringAssetPolicyBundleSelection(t *testing.T) {
	project := t.TempDir()
	child := filepath.Join(project, "skill")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{project, filepath.Dir(project), "/"} {
		f, err := p3SelectBundle(path, project)
		if f != nil {
			f.Close()
		}
		if !errors.Is(err, p3Forbidden) {
			t.Fatalf("accepted broad bundle: %v", err)
		}
	}
	f, err := p3SelectBundle(child, project)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
