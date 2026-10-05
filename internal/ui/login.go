package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/osac-project/osac-tui/internal/kubeauth"
)

type loginFinishedMsg struct {
	result kubeauth.Result
	err    error
}

type loginModel struct {
	input       textinput.Model
	needAddress bool
	needToken   bool
	busy        bool
	width       int
	height      int
	err         error
	result      *kubeauth.Result
}

// PromptLogin opens the startup login modal and returns the cluster details it
// discovers using the kubeconfig entered by the user.
func PromptLogin(needAddress, needToken bool) (kubeauth.Result, error) {
	input := textinput.New()
	input.Prompt = "Kubeconfig: "
	input.Placeholder = "blank uses current kubectl context"
	input.SetValue(kubeauth.DefaultKubeconfig())
	input.CharLimit = 1024
	input.Focus()

	model := loginModel{input: input, needAddress: needAddress, needToken: needToken}
	final, err := tea.NewProgram(model, tea.WithAltScreen()).Run()
	if err != nil {
		return kubeauth.Result{}, err
	}
	completed, ok := final.(loginModel)
	if !ok || completed.result == nil {
		return kubeauth.Result{}, errors.New("login canceled")
	}
	return *completed.result, nil
}

func (m loginModel) Init() tea.Cmd { return textinput.Blink }

func (m loginModel) loginCmd() tea.Cmd {
	kubeconfig := strings.TrimSpace(m.input.Value())
	needAddress := m.needAddress
	needToken := m.needToken
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		result, err := kubeauth.Login(ctx, kubeconfig, needAddress, needToken)
		return loginFinishedMsg{result: result, err: err}
	}
}

func (m loginModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		m.input.Width = maxInt(minInt(message.Width-24, 72), 12)
		return m, nil
	case loginFinishedMsg:
		m.busy = false
		if message.err != nil {
			m.err = message.err
			m.input.Focus()
			return m, nil
		}
		m.result = &message.result
		return m, tea.Quit
	case tea.KeyMsg:
		if message.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy {
			return m, nil
		}
		switch message.String() {
		case "esc":
			return m, tea.Quit
		case "enter":
			m.err = nil
			m.busy = true
			return m, m.loginCmd()
		}
	}
	if m.busy {
		return m, nil
	}
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	return m, command
}

func (m loginModel) View() string {
	if m.width == 0 {
		return ""
	}
	width := minInt(maxInt(m.width-4, 1), 94)
	input := m.input
	input.Width = maxInt(width-20, 1)
	body := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("OSAC LOGIN"),
		"",
		"Choose a kubeconfig for OSAC authentication.",
	}
	if m.needAddress {
		body = append(body, "The API address will be discovered from the cluster.")
	}
	if m.needToken {
		body = append(body, "A token will be created for the admin ServiceAccount in the OSAC namespace.")
	}
	body = append(body, "", input.View(), "", lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		"enter connect  |  blank uses current kubectl context  |  esc quit"))
	if m.busy {
		status := "Preparing OSAC connection..."
		switch {
		case m.needAddress && m.needToken:
			status = "Discovering API and creating admin token..."
		case m.needAddress:
			status = "Discovering OSAC API address..."
		case m.needToken:
			status = "Creating admin ServiceAccount token..."
		}
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render(status))
	}
	if m.err != nil {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("204")).Render(wrapText(m.err.Error(), maxInt(width-6, 1))))
	}
	dialog := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 1).
		Render(strings.Join(body, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}
