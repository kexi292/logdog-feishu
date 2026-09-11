package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kexi292/logdog-feishu/output"
)

func TestSaveLoadConfig(t *testing.T) {
	name := filepath.Join(t.TempDir(), "config.yaml")
	c := &Config{OutputHttp: &output.Http{Url: "https://example.invalid/hook"}, Inputs: []*Inputs{{Project: "mall", Name: "orders", Paths: []string{"/var/log/orders"}, IncludeLines: []string{"ERROR"}, ScanFrequency: 10}}}
	if err := Save(name, c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	got, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inputs[0].Project != "mall" || got.OutputHttp.Url != c.OutputHttp.Url {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestInvalidSavePreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := &Config{OutputHttp: &output.Http{Url: "https://example.invalid/hook"}, Inputs: []*Inputs{{Project: "demo", Name: "api", Paths: []string{"/var/log/example.log"}, IncludeLines: []string{"ERROR"}}}}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Inputs[0].ScanFrequency = -1
	if err := Save(path, c); err == nil {
		t.Fatal("invalid configuration was accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("failed save replaced original file")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatal("public credentials file accepted")
	}
}

func TestLoadRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for _, text := range []string{"unexpected: true\n", "inputs: []\n---\ninputs: []\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("ambiguous configuration accepted")
		}
	}
}
