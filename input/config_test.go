package input

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhjx922/alert/output"
)

func TestSaveLoadConfig(t *testing.T) {
	name := filepath.Join(t.TempDir(), "config.yaml")
	c := &Config{OutputHttp: &output.Http{Url: "https://example.invalid/hook", Method: "POST"}, Inputs: []*Inputs{{Project: "mall", Name: "orders", Paths: []string{"/var/log/orders"}, IncludeLines: []string{"ERROR"}, ScanFrequency: 10}}}
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
