package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kexi292/logdog-feishu/publisher"
)

func appendLog(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(text)
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func watchTest(t *testing.T, config *Inputs, store *cursorStore, reports *[]publisher.Report) *Input {
	t.Helper()
	in := newInput(config, store, "test-host", func(_ context.Context, r publisher.Report) error {
		*reports = append(*reports, r)
		return nil
	})
	t.Cleanup(func() { _ = in.close(context.Background(), false) })
	return in
}

func pollTest(t *testing.T, in *Input, now time.Time) {
	t.Helper()
	if err := in.poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

func TestContextResumeAndIsolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	before := "INFO old 1\nINFO old 2\nINFO old 3\nINFO old 4\nINFO old 5\n"
	appendLog(t, path, before)
	store, err := newCursorStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reports []publisher.Report
	a := watchTest(t, &Inputs{Project: "shop", Name: "api", Paths: []string{dir, path}, IncludeLines: []string{"ERROR", "Exception"}, ExcludeLines: []string{"ignored"}}, store, &reports)
	b := watchTest(t, &Inputs{Project: "ops", Name: "api", Paths: []string{path}, IncludeLines: []string{"ERROR"}}, store, &reports)
	now := time.Now()
	pollTest(t, a, now)
	pollTest(t, b, now)
	if len(reports) != 0 {
		t.Fatal("historical lines replayed")
	}
	appendLog(t, path, "ERR")
	pollTest(t, a, now)
	if got := store.data[cursorKey("shop", "api", path)].Offset; got != int64(len(before)) {
		t.Fatal("checkpoint skipped an unfinished line")
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if err := a.close(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	a = watchTest(t, a.config, reloaded, &reports)
	pollTest(t, a, now)
	stack := "ERROR Exception request=demo\n"
	for n := 0; n < 20; n++ {
		stack += fmt.Sprintf("\tat example.Frame%d.run(Frame.java:10)\n", n)
	}
	stack += "Caused by: ExampleException\n\tat nested.Frame.run(Frame.java:20)\n"
	after := "INFO next 1\nINFO next 2\nINFO next 3\nINFO next 4\nINFO next 5\n"
	appendLog(t, path, strings.TrimPrefix(stack, "ERR")+after)
	pollTest(t, a, now.Add(time.Second))
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	r := reports[0]
	if strings.Join(r.Lines, "") != before+stack+after {
		t.Fatal("context or multiline stack changed")
	}
	if r.File != path || r.Directory != dir || r.Project != "shop" || r.Service != "api" || r.Host != "test-host" || r.Rule != dir {
		t.Fatal("incorrect report source")
	}
	if r.Start != 0 || r.End != int64(len(before+stack+after)) || len(r.Keywords) != 2 {
		t.Fatal("incorrect range or keywords")
	}
	pollTest(t, b, now.Add(time.Second))
	if len(reports) != 2 || reports[1].Project != "ops" || r.ID() == reports[1].ID() {
		t.Fatal("services share a report or checkpoint")
	}
	if err := reloaded.save(); err != nil {
		t.Fatal(err)
	}
	_ = a.close(context.Background(), false)
	reloaded, err = newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	a = watchTest(t, a.config, reloaded, &reports)
	pollTest(t, a, now.Add(2*time.Second))
	appendLog(t, path, "ERROR ignored\n")
	pollTest(t, a, now.Add(3*time.Second))
	pollTest(t, a, now.Add(6*time.Second))
	if len(reports) != 2 {
		t.Fatal("successful report replayed or excluded line alerted")
	}
}

func TestRotationDeletionAndNewFiles(t *testing.T) {
	for _, kind := range []string{"rename", "truncate", "delete"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app.log")
			appendLog(t, path, strings.Repeat("old\n", 50))
			store, err := newCursorStore(filepath.Join(dir, "state"))
			if err != nil {
				t.Fatal(err)
			}
			var reports []publisher.Report
			in := watchTest(t, &Inputs{Project: "demo", Name: "api", Paths: []string{dir}, IncludeLines: []string{"ERROR"}, ScanFrequency: 1}, store, &reports)
			now := time.Now()
			pollTest(t, in, now)
			old := in.files[path].file.file
			appendLog(t, path, "ERROR old file\n\tframe\n")
			pollTest(t, in, now)
			switch kind {
			case "rename":
				err = os.Rename(path, path+".1")
			case "truncate":
				err = os.Truncate(path, 0)
			case "delete":
				err = os.Remove(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			pollTest(t, in, now.Add(time.Second))
			if _, err := old.Stat(); err == nil {
				t.Fatal("old file descriptor remains open")
			}
			if len(reports) != 1 || !strings.Contains(reports[0].Completeness, "incomplete") {
				t.Fatal("pending context lost at file change")
			}
			appendLog(t, path, "ERROR new file\n")
			pollTest(t, in, now.Add(2*time.Second))
			pollTest(t, in, now.Add(5*time.Second))
			if len(reports) != 2 || !strings.Contains(strings.Join(reports[1].Lines, ""), "ERROR new file") {
				t.Fatal("new file was skipped")
			}
			other := filepath.Join(dir, "new.log")
			appendLog(t, other, "ERROR discovered\n")
			pollTest(t, in, now.Add(6*time.Second))
			pollTest(t, in, now.Add(9*time.Second))
			if len(reports) != 3 || reports[2].File != other {
				t.Fatal("glob discovery skipped new content")
			}
		})
	}
}

func TestIdleLimitsAndShutdown(t *testing.T) {
	for _, mode := range []string{"idle", "partial", "line_limit", "report_limit", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "app.log")
			appendLog(t, path, "")
			store, err := newCursorStore(filepath.Join(dir, "state"))
			if err != nil {
				t.Fatal(err)
			}
			var reports []publisher.Report
			in := watchTest(t, &Inputs{Project: "demo", Name: "api", Paths: []string{path}, IncludeLines: []string{"ERROR"}}, store, &reports)
			now := time.Now()
			pollTest(t, in, now)
			text := "ERROR example\n"
			switch mode {
			case "partial":
				text = "ERROR unfinished"
			case "line_limit":
				text = "ERROR " + strings.Repeat("x", maxLineBytes*3) + "\n"
			case "report_limit":
				text += strings.Repeat("\tat frame\n", maxReportLines+10)
			}
			appendLog(t, path, text)
			for n := 0; n < 6; n++ {
				pollTest(t, in, now)
			}
			if mode == "shutdown" {
				if err := in.close(context.Background(), true); err != nil {
					t.Fatal(err)
				}
			} else {
				pollTest(t, in, now.Add(3*time.Second))
				pollTest(t, in, now.Add(6*time.Second))
			}
			if len(reports) != 1 {
				t.Fatalf("reports=%d", len(reports))
			}
			r := reports[0]
			if r.Completeness == "complete" || r.Completeness == "" {
				t.Fatal("overclaimed completeness")
			}
			if mode == "idle" && strings.Join(r.Lines, "") != text {
				t.Fatal("idle context changed")
			}
			if mode == "line_limit" && !strings.Contains(r.Text(), "line exceeds") {
				t.Fatal("line truncation not disclosed")
			}
			if mode == "report_limit" && (!strings.Contains(r.Completeness, "collection limit") || len(r.Lines) > maxReportLines) {
				t.Fatal("report limit not enforced")
			}
		})
	}
}

func TestFailureKeepsCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendLog(t, path, "old\n")
	store, err := newCursorStore(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var reports []publisher.Report
	in := watchTest(t, &Inputs{Project: "demo", Name: "api", Paths: []string{path}, IncludeLines: []string{"ERROR"}}, store, &reports)
	now := time.Now()
	pollTest(t, in, now)
	in.send = func(context.Context, publisher.Report) error { return errors.New("offline") }
	appendLog(t, path, "ERROR retry\n")
	pollTest(t, in, now)
	if err := in.poll(context.Background(), now.Add(3*time.Second)); err == nil {
		t.Fatal("send failure swallowed")
	}
	if got := store.data[cursorKey("demo", "api", path)].Offset; got != 4 {
		t.Fatal("checkpoint passed unsent event")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.replay(context.Background(), func(_ context.Context, r publisher.Report) error {
		reports = append(reports, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !strings.Contains(reports[0].Text(), "ERROR retry") {
		t.Fatal("unsent report lost after source deletion")
	}
	reloaded, err = newCursorStore(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.replay(context.Background(), func(context.Context, publisher.Report) error { t.Fatal("acknowledged report replayed"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestTwoProjectsTwoServicesTwoPaths(t *testing.T) {
	dir := t.TempDir()
	store, err := newCursorStore(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var reports []publisher.Report
	var inputs []*Input
	now := time.Now()
	for project := 0; project < 2; project++ {
		for service := 0; service < 2; service++ {
			config := &Inputs{Project: fmt.Sprintf("project-%d", project), Name: fmt.Sprintf("service-%d", service), IncludeLines: []string{fmt.Sprintf("problem-%d-%d", project, service)}}
			for path := 0; path < 2; path++ {
				name := filepath.Join(dir, fmt.Sprintf("%d-%d-%d.log", project, service, path))
				appendLog(t, name, "")
				config.Paths = append(config.Paths, name)
			}
			in := watchTest(t, config, store, &reports)
			pollTest(t, in, now)
			inputs = append(inputs, in)
		}
	}
	for _, in := range inputs {
		for _, path := range in.config.Paths {
			appendLog(t, path, in.config.IncludeLines[0]+"\nINFO 1\nINFO 2\nINFO 3\nINFO 4\nINFO 5\n")
		}
		pollTest(t, in, now)
	}
	if len(reports) != 8 || len(store.data) != 8 {
		t.Fatal("multi-project paths or checkpoints were dropped")
	}
	seen := map[string]bool{}
	for _, r := range reports {
		key := cursorKey(r.Project, r.Service, r.File)
		if seen[key] {
			t.Fatal("duplicate service file report")
		}
		seen[key] = true
		if _, ok := store.data[key]; !ok {
			t.Fatal("report is associated with another service checkpoint")
		}
	}
}
