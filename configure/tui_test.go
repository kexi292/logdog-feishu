package configure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kexi292/logdog-feishu/input"
	"github.com/kexi292/logdog-feishu/output"
)

func TestMenuPreservesServiceAndRejectsInvalidScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	c := &input.Config{OutputHttp: &output.Http{Url: "https://example.invalid/private-placeholder"}, Inputs: []*input.Inputs{{Project: "demo", Name: "first", Paths: []string{"/var/log/example.log"}, IncludeLines: []string{"ERROR"}, ScanFrequency: 10}}}
	m := newModel(c, path)
	if strings.Contains(m.View(), "private-placeholder") {
		t.Fatal("webhook visible in TUI")
	}
	m.moveFocus(len(m.fields) + 1)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.service != 1 || len(m.config.Inputs) != 2 {
		t.Fatal("add-service menu action lost its updated state")
	}
	m.fields[2].SetValue("demo")
	m.fields[3].SetValue("second")
	m.fields[4].SetValue("/var/log/second.log")
	m.fields[5].SetValue("ERROR")
	m.fields[7].SetValue("invalid")
	m.moveFocus(len(m.fields))
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.err == "" {
		t.Fatal("invalid scan interval silently accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid configuration was saved")
	}
	m.fields[7].SetValue("5")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.err != "" {
		t.Fatal(m.err)
	}
	saved, err := input.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Inputs[0].Name != "first" || saved.Inputs[1].Name != "second" || saved.Inputs[1].ScanFrequency != 5 {
		t.Fatal("service data crossed during editing")
	}
	m.moveFocus(len(m.fields) + 5)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !m.config.ServerMonitor.Enabled || !strings.Contains(m.View(), "Disable server monitoring") {
		t.Fatal("server monitoring toggle lost")
	}
	m.fields[8].SetValue("1.5")
	m.fields[9].SetValue("92")
	m.moveFocus(len(m.fields))
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	saved, err = input.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ServerMonitor == nil || !saved.ServerMonitor.Enabled || saved.ServerMonitor.LoadPerCPU != 1.5 || saved.ServerMonitor.MemoryPercent != 92 {
		t.Fatal("server settings did not survive saving")
	}
	m.fields[9].SetValue("NaN")
	m.saveConfig()
	if m.err == "" {
		t.Fatal("invalid server threshold accepted by TUI")
	}
	m.err, m.status = "", ""
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(model)
	view := m.View()
	if len(strings.Split(view, "\n")) > 24 {
		t.Fatal("menu does not fit a standard terminal")
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatal("TUI wraps beyond the terminal width")
		}
	}
}
