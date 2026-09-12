package configure

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kexi292/logdog-feishu/input"
	"github.com/kexi292/logdog-feishu/output"
	"golang.org/x/term"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

type model struct {
	config         *input.Config
	filename       string
	fields         []textinput.Model
	focus, service int
	err, status    string
}

var actionLabels = []string{"Save configuration", "Add service", "Previous service", "Next service", "Quit", "Enable server monitoring"}

var fieldLabels = []string{"Webhook URL", "Signing secret", "Project", "Service", "Paths (comma separated)", "Include keywords", "Exclude keywords", "Scan seconds", "Server load/core threshold", "Server memory threshold (%)"}

func Run(filename string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("configure requires an interactive terminal; use run for background monitoring")
	}
	c, err := input.Load(filename)
	if os.IsNotExist(err) {
		c = &input.Config{OutputHttp: &output.Http{}}
	} else if err != nil {
		return err
	}
	if c.OutputHttp == nil {
		c.OutputHttp = &output.Http{}
	}
	if len(c.Inputs) == 0 {
		c.Inputs = []*input.Inputs{{ScanFrequency: 10}}
	}
	for _, in := range c.Inputs {
		if in == nil {
			return fmt.Errorf("configuration contains an empty service; remove the empty entry before editing")
		}
	}
	_, err = tea.NewProgram(newModel(c, filename)).Run()
	return err
}

func newModel(c *input.Config, filename string) model {
	m := model{config: c, filename: filename, fields: make([]textinput.Model, len(fieldLabels))}
	m.loadService(0)
	return m
}

func (m *model) loadService(index int) {
	in := m.config.Inputs[index]
	server := input.ServerConfig{LoadPerCPU: 1, MemoryPercent: 90}
	if m.config.ServerMonitor != nil {
		server = *m.config.ServerMonitor
	}
	values := []string{m.config.OutputHttp.Url, m.config.OutputHttp.Secret, in.Project, in.Name, strings.Join(in.Paths, ", "), strings.Join(in.IncludeLines, ", "), strings.Join(in.ExcludeLines, ", "), strconv.FormatInt(in.ScanFrequency, 10), strconv.FormatFloat(server.LoadPerCPU, 'f', -1, 64), strconv.FormatFloat(server.MemoryPercent, 'f', -1, 64)}
	for i := range m.fields {
		m.fields[i] = textinput.New()
		m.fields[i].Prompt = ""
		m.fields[i].Placeholder = fieldLabels[i]
		m.fields[i].SetValue(values[i])
		m.fields[i].Width = 40
		if i <= 1 {
			m.fields[i].EchoMode = textinput.EchoPassword
		}
	}
	m.service, m.focus = index, 0
	m.fields[0].Focus()
}

func (m *model) moveFocus(next int) {
	if m.focus < len(m.fields) {
		m.fields[m.focus].Blur()
	}
	m.focus = (next + len(m.fields) + len(actionLabels)) % (len(m.fields) + len(actionLabels))
	if m.focus < len(m.fields) {
		m.fields[m.focus].Focus()
	}
}

func (m *model) saveConfig() {
	if !m.saveFields() {
		return
	}
	if err := input.Save(m.filename, m.config); err != nil {
		m.err, m.status = err.Error(), ""
	} else {
		m.err, m.status = "", "saved "+m.filename
	}
}

func (m *model) chooseAction() tea.Cmd {
	switch m.focus - len(m.fields) {
	case 0:
		m.saveConfig()
	case 1:
		if !m.saveFields() {
			return nil
		}
		m.config.Inputs = append(m.config.Inputs, &input.Inputs{ScanFrequency: 10})
		m.loadService(len(m.config.Inputs) - 1)
		m.status, m.err = "new service", ""
	case 2:
		if m.service > 0 {
			if !m.saveFields() {
				return nil
			}
			m.loadService(m.service - 1)
		}
	case 3:
		if m.service+1 < len(m.config.Inputs) {
			if !m.saveFields() {
				return nil
			}
			m.loadService(m.service + 1)
		}
	case 4:
		return tea.Quit
	case 5:
		if !m.saveFields() {
			return nil
		}
		m.config.ServerMonitor.Enabled = !m.config.ServerMonitor.Enabled
		m.err = ""
		m.status = "Server monitoring changed; select Save configuration to apply on restart"
	}
	return nil
}

func (m *model) saveFields() bool {
	n, err := strconv.ParseInt(strings.TrimSpace(m.fields[7].Value()), 10, 64)
	if err != nil || n < 0 || n > 3600 {
		m.err, m.status = "Scan seconds must be 0 (default) or 1..3600", ""
		return false
	}
	load, loadErr := strconv.ParseFloat(strings.TrimSpace(m.fields[8].Value()), 64)
	memory, memoryErr := strconv.ParseFloat(strings.TrimSpace(m.fields[9].Value()), 64)
	server := input.ServerConfig{LoadPerCPU: load, MemoryPercent: memory}
	if m.config.ServerMonitor != nil {
		server.Enabled = m.config.ServerMonitor.Enabled
	}
	if loadErr != nil || memoryErr != nil {
		m.err, m.status = "Server thresholds must be numbers", ""
		return false
	}
	if err := server.Validate(); err != nil {
		m.err, m.status = err.Error(), ""
		return false
	}
	c := m.config
	c.ServerMonitor = &server
	h := c.OutputHttp
	h.Url, h.Secret = strings.TrimSpace(m.fields[0].Value()), m.fields[1].Value()
	in := c.Inputs[m.service]
	in.Project, in.Name = strings.TrimSpace(m.fields[2].Value()), strings.TrimSpace(m.fields[3].Value())
	in.Paths, in.IncludeLines, in.ExcludeLines = split(m.fields[4].Value()), split(m.fields[5].Value()), split(m.fields[6].Value())
	in.ScanFrequency = n
	return true
}
func split(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		for n := range m.fields {
			width := size.Width - lipgloss.Width(fieldLabels[n]) - 6
			if width < 10 {
				width = 10
			}
			m.fields[n].Width = width
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "down":
			m.moveFocus(m.focus + 1)
		case "shift+tab", "up":
			m.moveFocus(m.focus - 1)
		case "enter":
			if m.focus >= len(m.fields) {
				cmd := m.chooseAction()
				return m, cmd
			}
		}
	}
	if m.focus >= len(m.fields) {
		return m, nil
	}
	var cmd tea.Cmd
	m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
	return m, cmd
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("LOGDOG FEISHU  /  CONFIGURE") + "\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("Service %d/%d   |   Move with Tab or up/down, press Enter on an action", m.service+1, len(m.config.Inputs))) + "\n\n")
	for i, label := range fieldLabels {
		marker := "  "
		if i == m.focus {
			marker = "> "
		}
		b.WriteString(marker + label + ": " + m.fields[i].View() + "\n")
	}
	b.WriteString("Actions:\n")
	for i, label := range actionLabels {
		if i == 5 && m.config.ServerMonitor != nil && m.config.ServerMonitor.Enabled {
			label = "Disable server monitoring"
		}
		marker := "  "
		if m.focus == len(m.fields)+i {
			marker = ">> "
		}
		b.WriteString(marker + label + "\n")
	}
	if m.err != "" {
		b.WriteString(errorStyle.Render("Error: "+m.err) + "\n")
	}
	if m.status != "" {
		b.WriteString(mutedStyle.Render(m.status) + "\n")
	}
	b.WriteString("\n" + mutedStyle.Render("Comma-separate paths and keywords. Server settings apply to this host."))
	return b.String()
}
