package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const menuLogo = ` __  __       _
|  \/  | ___ | | ___
| |\/| |/ _ \| |/ _ \
| |  | | (_) | |  __/
|_|  |_|\___/|_|\___|`

type menuItem struct {
	command string
	title   string
	desc    string
}

var menuItems = []menuItem{
	{"clean", "Clean", "Free up disk space"},
	{"uninstall", "Uninstall", "Remove apps completely"},
	{"analyze", "Analyze", "Explore disk usage"},
	{"status", "Status", "Monitor system health"},
}

var (
	menuGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("71"))
	menuBlue  = lipgloss.NewStyle().Foreground(lipgloss.Color("74")).Bold(true)
	menuCyan  = lipgloss.NewStyle().Foreground(lipgloss.Color("80"))
	menuDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// menuModel is the interactive launcher shown when mole runs without arguments
// in a terminal. It only picks a command; the command runs after the menu quits.
type menuModel struct {
	cursor int
	choice string
	showV  bool
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch s := key.String(); s {
	case "ctrl+c", "q", "Q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = (m.cursor + len(menuItems) - 1) % len(menuItems)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(menuItems)
	case "enter":
		m.choice = menuItems[m.cursor].command
		return m, tea.Quit
	case "v", "V":
		m.showV = !m.showV
	default:
		if len(s) == 1 && s[0] >= '1' && int(s[0]-'0') <= len(menuItems) {
			m.choice = menuItems[s[0]-'1'].command
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m menuModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	logo := strings.Split(menuLogo, "\n")
	for i, line := range logo {
		b.WriteString(menuGreen.Render(line))
		switch i {
		case 3:
			b.WriteString("  " + menuBlue.Render("https://github.com/tw93/mole"))
		case 4:
			b.WriteString("  " + menuGreen.Render("Deep clean and optimize your PC."))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for i, it := range menuItems {
		label := fmt.Sprintf("%d. %-10s", i+1, it.title)
		if i == m.cursor {
			b.WriteString(menuCyan.Render("➤ "+label+" "+it.desc) + "\n")
		} else {
			b.WriteString("  " + label + " " + it.desc + "\n")
		}
	}
	b.WriteString("\n")
	if m.showV {
		b.WriteString(menuDim.Render("Mole version "+version) + "\n")
	} else {
		b.WriteString(menuDim.Render("↑↓ | Enter | V Version | Q Quit") + "\n")
	}
	return b.String()
}

// isTerminal reports whether f is an interactive character device.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runMenu shows the launcher and returns the chosen command, or "" when the
// user quit without choosing.
func runMenu(in io.Reader, out io.Writer) (string, error) {
	final, err := tea.NewProgram(menuModel{}, tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return "", err
	}
	return final.(menuModel).choice, nil
}
