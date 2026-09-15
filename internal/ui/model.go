package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/osac-project/osac-tui/internal/client"
	"github.com/osac-project/osac-tui/internal/yamlcodec"
	"google.golang.org/protobuf/proto"
)

type screen uint8

const (
	listScreen screen = iota
	detailScreen
)

type rowsLoadedMsg struct {
	rows []client.Row
	err  error
}

type objectLoadedMsg struct {
	object proto.Message
	data   []byte
	err    error
}

type mutationFinishedMsg struct {
	object proto.Message
	data   []byte
	err    error
}

type deleteFinishedMsg struct{ err error }

type editorFinishedMsg struct {
	data []byte
	err  error
}

var actionVerbs = map[string]string{
	"create": "Create",
	"update": "Update",
}

var actionProgress = map[string]string{
	"create": "Creating...",
	"update": "Updating...",
}

type Model struct {
	resource  client.Resource
	resources []client.Resource
	rows      []client.Row
	selected  int

	screen        screen
	resourceMenu  bool
	resourceIndex int
	confirmDelete bool
	loading       bool

	object   proto.Message
	objectID string
	action   string

	viewport viewport.Model
	width    int
	height   int
	status   string
	err      error
}

func New(api *client.Client) Model {
	resources := api.Resources()
	model := Model{
		resources: resources,
		resource:  resources[0],
		viewport:  viewport.New(0, 0),
		status:    "Loading resources...",
	}
	model.loading = true
	return model
}

func (m Model) Init() tea.Cmd { return m.loadRowsCmd() }

func (m Model) loadRowsCmd() tea.Cmd {
	resource := m.resource
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := resource.List(ctx)
		return rowsLoadedMsg{rows: rows, err: err}
	}
}

func (m Model) loadObjectCmd(id string) tea.Cmd {
	resource := m.resource
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		object, err := resource.Get(ctx, id)
		if err != nil {
			return objectLoadedMsg{err: err}
		}
		data, err := yamlcodec.Marshal(object)
		return objectLoadedMsg{object: object, data: data, err: err}
	}
}

func (m Model) mutationCmd(object proto.Message) tea.Cmd {
	resource := m.resource
	action := m.action
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var (
			result proto.Message
			err    error
		)
		if action == "create" {
			result, err = resource.Create(ctx, object)
		}
		if action == "update" {
			result, err = resource.Update(ctx, object)
		}
		if err != nil {
			return mutationFinishedMsg{err: err}
		}
		data, err := yamlcodec.Marshal(result)
		return mutationFinishedMsg{object: result, data: data, err: err}
	}
}

func (m Model) deleteCmd(id string) tea.Cmd {
	resource := m.resource
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return deleteFinishedMsg{err: resource.Delete(ctx, id)}
	}
}

func (m Model) editCmd(data []byte) (tea.Cmd, error) {
	file, err := os.CreateTemp("", "osac-tui-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create editor file: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(data); err != nil {
		cleanup()
		return nil, fmt.Errorf("write editor file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, fmt.Errorf("close editor file: %w", err)
	}

	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	command := exec.Command("sh", "-c", editor+" \"$1\"", "osac-tui-editor", path)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		if err != nil {
			cleanup()
			return editorFinishedMsg{err: fmt.Errorf("run editor %q: %w", editor, err)}
		}
		data, readErr := os.ReadFile(path)
		cleanup()
		if readErr != nil {
			return editorFinishedMsg{err: fmt.Errorf("read editor file: %w", readErr)}
		}
		return editorFinishedMsg{data: data}
	}), nil
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		m.viewport.Width = message.Width - 6
		m.viewport.Height = message.Height - 9
		return m, nil
	case rowsLoadedMsg:
		m.loading = false
		m.rows = message.rows
		m.selected = clamp(m.selected, len(m.rows))
		m.err = message.err
		if message.err != nil {
			m.status = "List failed"
			return m, nil
		}
		m.status = fmt.Sprintf("%d %s", len(m.rows), m.resource.Title())
		return m, nil
	case objectLoadedMsg:
		m.loading = false
		m.err = message.err
		if message.err != nil {
			m.status = "Get failed"
			return m, nil
		}
		m.object = message.object
		m.viewport.SetContent(string(message.data))
		m.viewport.GotoTop()
		m.screen = detailScreen
		m.status = "Read-only view"
		return m, nil
	case mutationFinishedMsg:
		m.loading = false
		m.err = message.err
		if message.err != nil {
			m.status = "Mutation failed"
			return m, nil
		}
		m.object = message.object
		m.objectID = m.resource.Row(message.object).ID
		m.viewport.SetContent(string(message.data))
		m.viewport.GotoTop()
		m.screen = detailScreen
		m.status = actionVerbs[m.action] + " completed"
		return m, m.loadRowsCmd()
	case deleteFinishedMsg:
		m.loading = false
		m.confirmDelete = false
		m.err = message.err
		if message.err != nil {
			m.status = "Delete failed"
			return m, nil
		}
		m.screen = listScreen
		m.status = "Delete completed"
		return m, m.loadRowsCmd()
	case editorFinishedMsg:
		if message.err != nil {
			m.err = message.err
			m.status = "Editor failed"
			return m, nil
		}
		object := m.resource.New()
		if err := yamlcodec.Unmarshal(message.data, object); err != nil {
			m.err = err
			m.status = "Invalid YAML"
			return m, nil
		}
		m.object = object
		m.loading = true
		m.status = actionProgress[m.action]
		return m, m.mutationCmd(object)
	}

	if m.loading {
		if key, ok := message.(tea.KeyMsg); ok && (key.String() == "q" || key.String() == "ctrl+c") {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.resourceMenu {
		return m.updateResourceMenu(message)
	}
	if m.screen == detailScreen {
		return m.updateDetail(message)
	}
	return m.updateList(message)
}

func (m Model) updateList(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		m.resourceMenu = true
		m.resourceIndex = resourceIndex(m.resources, m.resource.Key())
	case "up", "k":
		m.selected = clamp(m.selected-1, len(m.rows))
	case "down", "j":
		m.selected = clamp(m.selected+1, len(m.rows))
	case "enter":
		if len(m.rows) == 0 {
			return m, nil
		}
		m.loading = true
		m.objectID = m.rows[m.selected].ID
		m.status = "Loading object..."
		return m, m.loadObjectCmd(m.objectID)
	case "r":
		m.loading = true
		m.status = "Refreshing..."
		return m, m.loadRowsCmd()
	case "c":
		if !m.resource.Writable() {
			m.status = "Resource is read-only"
			return m, nil
		}
		m.action = "create"
		m.object = m.resource.New()
		data, err := yamlcodec.Marshal(m.object)
		if err != nil {
			m.err = err
			return m, nil
		}
		command, err := m.editCmd(data)
		if err != nil {
			m.err = err
			m.status = "Editor failed"
			return m, nil
		}
		m.status = "Opening editor..."
		return m, command
	}
	return m, nil
}

func (m Model) updateDetail(message tea.Msg) (tea.Model, tea.Cmd) {
	if viewportMessage, ok := message.(tea.KeyMsg); ok {
		switch viewportMessage.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc", "backspace":
			m.screen = listScreen
			m.status = fmt.Sprintf("%d %s", len(m.rows), m.resource.Title())
		case "e":
			if !m.resource.Writable() {
				m.status = "Resource is read-only"
				return m, nil
			}
			data, err := yamlcodec.Marshal(m.object)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.action = "update"
			command, err := m.editCmd(data)
			if err != nil {
				m.err = err
				m.status = "Editor failed"
				return m, nil
			}
			m.status = "Opening editor..."
			return m, command
		case "d":
			if !m.resource.Writable() {
				m.status = "Resource is read-only"
				return m, nil
			}
			m.confirmDelete = true
		case "r":
			m.loading = true
			m.status = "Refreshing..."
			return m, m.loadObjectCmd(m.objectID)
		}
	}
	if m.confirmDelete {
		return m.updateDeleteConfirmation(message)
	}
	var command tea.Cmd
	m.viewport, command = m.viewport.Update(message)
	return m, command
}

func (m Model) updateDeleteConfirmation(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y":
		m.loading = true
		m.status = "Deleting..."
		return m, m.deleteCmd(m.objectID)
	case "n", "esc":
		m.confirmDelete = false
	}
	return m, nil
}

func (m Model) updateResourceMenu(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc", "tab":
		m.resourceMenu = false
	case "up", "k":
		m.resourceIndex = clamp(m.resourceIndex-1, len(m.resources))
	case "down", "j":
		m.resourceIndex = clamp(m.resourceIndex+1, len(m.resources))
	case "enter":
		m.resource = m.resources[m.resourceIndex]
		m.resourceMenu = false
		m.selected = 0
		m.loading = true
		m.status = "Loading resources..."
		return m, m.loadRowsCmd()
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	var body string
	switch m.screen {
	case listScreen:
		body = m.listView()
	case detailScreen:
		body = m.detailView()
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("OSAC TUI")
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252")).Render(m.resource.Title())
	top := lipgloss.JoinHorizontal(lipgloss.Left, header, "  ", title)
	footer := m.footerView()
	return lipgloss.JoinVertical(lipgloss.Left, top, "", body, "", footer)
}

func (m Model) listView() string {
	columns := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("241"))
	line := fmt.Sprintf("%-4s %-24s %-18s %-28s %s", "", "NAME", "STATE", "ID", "VERSION")
	lines := []string{columns.Render(line)}
	start, end := visibleRows(m.selected, len(m.rows), m.height-11)
	if start > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  ^ more above"))
	}
	for index := start; index < end; index++ {
		row := m.rows[index]
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if index == m.selected {
			cursor = "> "
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))
		}
		lines = append(lines, style.Render(fmt.Sprintf("%-4s %-24s %-18s %-28s %s", cursor, row.Name, row.State, row.ID, row.Version)))
	}
	if end < len(m.rows) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  v more below"))
	}
	if len(m.rows) == 0 && !m.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  No resources found"))
	}
	if m.resourceMenu {
		lines = append(lines, "", m.resourceMenuView())
	}
	return strings.Join(lines, "\n")
}

func (m Model) resourceMenuView() string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render("RESOURCE KINDS")}
	for index, resource := range m.resources {
		cursor := "  "
		if index == m.resourceIndex {
			cursor = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%s", cursor, resource.Title()))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("205")).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) detailView() string {
	if m.confirmDelete {
		warning := lipgloss.NewStyle().Foreground(lipgloss.Color("204")).Bold(true).Render("Delete this object? [y/N]")
		return lipgloss.JoinVertical(lipgloss.Left, warning, "", m.viewport.View())
	}
	return m.viewport.View()
}

func (m Model) footerView() string {
	status := m.status
	if m.err != nil {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("204")).Render(m.err.Error())
	}
	keyText := "tab resources  enter view  r refresh  q quit"
	if m.resource.Writable() {
		keyText = "tab resources  c create  enter view  e edit  d delete  r refresh  q quit"
	}
	keys := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(keyText)
	return lipgloss.JoinHorizontal(lipgloss.Left, status, "    ", keys)
}

func clamp(value, length int) int {
	if length == 0 {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value >= length {
		return length - 1
	}
	return value
}

func resourceIndex(resources []client.Resource, key string) int {
	for index, resource := range resources {
		if resource.Key() == key {
			return index
		}
	}
	panic("resource is not registered: " + key)
}

func visibleRows(selected, total, capacity int) (int, int) {
	if total == 0 {
		return 0, 0
	}
	if capacity < 1 {
		capacity = 1
	}
	if capacity >= total {
		return 0, total
	}
	start := selected - capacity + 1
	if start < 0 {
		start = 0
	}
	return start, start + capacity
}
