package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kexi292/logdog-feishu/output"
	"github.com/kexi292/logdog-feishu/publisher"
)

func TestRunCheckpointLockAndCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	state := filepath.Join(dir, "state")
	appendLog(t, path, "")
	config := &Config{OutputHttp: &output.Http{Url: "https://example.invalid/hook"}, Inputs: []*Inputs{{Project: "demo", Name: "api", Paths: []string{path}, IncludeLines: []string{"ERROR"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	reports := make(chan publisher.Report, 1)
	go func() {
		done <- Run(ctx, config, state, func(_ context.Context, r publisher.Report) error { reports <- r; cancel(); return nil })
	}()
	t.Cleanup(func() { cancel() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(state); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("listener stopped before initialization: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never initialized")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := Run(context.Background(), config, state, func(context.Context, publisher.Report) error { return nil }); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatal("second process can overwrite state")
	}
	text := "ERROR runtime\nINFO 1\nINFO 2\nINFO 3\nINFO 4\nINFO 5\n"
	appendLog(t, path, text)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not stop on cancellation")
	}
	select {
	case r := <-reports:
		if !strings.Contains(r.Text(), "ERROR runtime") {
			t.Fatal("report body missing")
		}
	default:
		t.Fatal("listener did not publish")
	}
	saved, err := newCursorStore(state)
	if err != nil {
		t.Fatal(err)
	}
	c := saved.data[cursorKey("demo", "api", path)]
	if c.Offset != int64(len(text)) || c.Pending != nil {
		t.Fatal("final checkpoint not acknowledged")
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("state containing report content is not private")
	}
}

func TestCorruptCursorDoesNotReset(t *testing.T) {
	for _, body := range []string{`{`, `null`, `{"x":{"identity":"1:2","offset":-1}}`} {
		path := filepath.Join(t.TempDir(), "state")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := newCursorStore(path); err == nil {
			t.Fatal("corrupt state silently reset")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Fatal("corrupt state overwritten")
		}
	}
}
