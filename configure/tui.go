package configure

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zhjx922/alert/input"
	"github.com/zhjx922/alert/output"
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

var actionLabels = []string{"Save configuration", "Add service", "Previous service", "Next service", "Quit"}

func Run(filename string) error {
	c, err := input.Load(filename)
	if os.IsNotExist(err) {
		c = &input.Config{OutputHttp: &output.Http{Method: "POST", Format: "json"}}
	} else if err != nil {
		return err
	}
	if c.OutputHttp == nil {
		c.OutputHttp = &output.Http{Method: "POST", Format: "json"}
	}
	if len(c.Inputs) == 0 {
		c.Inputs = []*input.Inputs{{ScanFrequency: 10}}
	}
	_, err = tea.NewProgram(newModel(c, filename)).Run()
	return err
}

func newModel(c *input.Config, filename string) model {
	m := model{config: c, filename: filename, fields: make([]textinput.Model, 8)}
	m.loadService(0)
	return m
}

func (m *model) loadService(index int) {
	in := m.config.Inputs[index]
	values := []string{m.config.OutputHttp.Url, m.config.OutputHttp.Secret, in.Project, in.Name, strings.Join(in.Paths, ", "), strings.Join(in.IncludeLines, ", "), strings.Join(in.ExcludeLines, ", "), strconv.FormatInt(in.ScanFrequency, 10)}
	labels := []string{"Webhook URL", "Signing secret", "Project", "Service", "Paths (comma separated)", "Include keywords", "Exclude keywords", "Scan seconds"}
	for i := range m.fields {
		m.fields[i] = textinput.New()
		m.fields[i].Prompt = ""
		m.fields[i].Placeholder = labels[i]
		m.fields[i].SetValue(values[i])
		m.fields[i].Width = 70
		if i == 1 {
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
	m.saveFields()
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
		m.saveFields()
		m.config.Inputs = append(m.config.Inputs, &input.Inputs{ScanFrequency: 10})
		m.loadService(len(m.config.Inputs) - 1)
		m.status, m.err = "new service", ""
	case 2:
		if m.service > 0 {
			m.saveFields()
			m.loadService(m.service - 1)
		}
	case 3:
		if m.service+1 < len(m.config.Inputs) {
			m.saveFields()
			m.loadService(m.service + 1)
		}
	case 4:
		return tea.Quit
	}
	return nil
}

func (m *model) saveFields() {
	c := m.config
	h := c.OutputHttp
	h.Url, h.Secret = strings.TrimSpace(m.fields[0].Value()), m.fields[1].Value()
	in := c.Inputs[m.service]
	in.Project, in.Name = strings.TrimSpace(m.fields[2].Value()), strings.TrimSpace(m.fields[3].Value())
	in.Paths, in.IncludeLines, in.ExcludeLines = split(m.fields[4].Value()), split(m.fields[5].Value()), split(m.fields[6].Value())
	if n, err := strconv.ParseInt(strings.TrimSpace(m.fields[7].Value()), 10, 64); err == nil {
		in.ScanFrequency = n
	}
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
				return m, m.chooseAction()
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
	labels := []string{"Webhook URL", "Signing secret", "Project", "Service", "Paths (comma separated)", "Include keywords", "Exclude keywords", "Scan seconds"}
	var b strings.Builder
	b.WriteString(titleStyle.Render("LOGDOG FEISHU  /  CONFIGURE") + "\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("Service %d/%d   |   Move with Tab or up/down, press Enter on an action", m.service+1, len(m.config.Inputs))) + "\n\n")
	for i, label := range labels {
		b.WriteString(label + ":\n" + m.fields[i].View() + "\n\n")
	}
	b.WriteString("Actions:\n")
	for i, label := range actionLabels {
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
	b.WriteString("\n" + mutedStyle.Render("Comma-separated paths and keywords are saved as lists. Saving validates the whole file."))
	return b.String()
}
