package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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
	resource    client.Resource
	resources   []client.Resource
	connection  client.ConnectionInfo
	tuiVersion  string
	osacVersion string
	rows        []client.Row
	selected    int

	screen        screen
	resourceMenu  bool
	resourceIndex int
	confirmDelete bool
	loading       bool

	object   proto.Message
	objectID string
	action   string

	viewport       viewport.Model
	commandInput   textinput.Model
	resourceSearch textinput.Model
	commandMode    bool
	width          int
	height         int
	status         string
	err            error
	errorDialog    bool
}

func New(api *client.Client, tuiVersion, osacVersion string) Model {
	resources := api.Resources()
	commandInput := textinput.New()
	commandInput.Prompt = ":"
	commandInput.CharLimit = 64
	commandInput.ShowSuggestions = true
	resourceSearch := textinput.New()
	resourceSearch.Prompt = "/"
	resourceSearch.CharLimit = 64
	resourceSearch.ShowSuggestions = true
	model := Model{
		resources:      resources,
		resource:       resources[0],
		connection:     api.ConnectionInfo(),
		tuiVersion:     tuiVersion,
		osacVersion:    osacVersion,
		viewport:       viewport.New(0, 0),
		commandInput:   commandInput,
		resourceSearch: resourceSearch,
		status:         "Loading resources...",
	}
	model.commandInput.SetSuggestions(resourceCompletionSuggestions(resources, ""))
	model.resourceSearch.SetSuggestions(resourceCompletionSuggestions(resources, ""))
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
		m.viewport.Width = maxInt(message.Width-6, 1)
		m.viewport.Height = m.bodyHeight()
		m.commandInput.Width = maxInt(message.Width-8, 1)
		m.resourceSearch.Width = maxInt(message.Width/3-6, 1)
		return m, nil
	case rowsLoadedMsg:
		m.loading = false
		m.rows = message.rows
		m.selected = clamp(m.selected, len(m.rows))
		m.err = message.err
		if message.err != nil {
			m.status = "Unavailable on this server"
			return m, nil
		}
		m.err = nil
		m.status = fmt.Sprintf("%d %s", len(m.rows), m.resource.Title())
		return m, nil
	case objectLoadedMsg:
		m.loading = false
		m.err = message.err
		if message.err != nil {
			m.errorDialog = true
			m.status = "Unable to read object"
			return m, nil
		}
		m.err = nil
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
			m.errorDialog = true
			m.status = "Unable to save object"
			return m, nil
		}
		m.err = nil
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
			m.errorDialog = true
			m.status = "Unable to delete object"
			return m, nil
		}
		m.err = nil
		m.screen = listScreen
		m.status = "Delete completed"
		return m, m.loadRowsCmd()
	case editorFinishedMsg:
		if message.err != nil {
			m.err = message.err
			m.errorDialog = true
			m.status = "Editor failed"
			return m, nil
		}
		m.err = nil
		object := m.resource.New()
		if err := yamlcodec.Unmarshal(message.data, object); err != nil {
			m.err = err
			m.errorDialog = true
			m.status = "Invalid YAML"
			return m, nil
		}
		m.object = object
		m.loading = true
		m.status = actionProgress[m.action]
		return m, m.mutationCmd(object)
	}
	if m.errorDialog {
		key, ok := message.(tea.KeyMsg)
		if !ok {
			return m, nil
		}
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter", "esc":
			m.errorDialog = false
			m.err = nil
		}
		return m, nil
	}
	if m.commandMode {
		return m.updateCommand(message)
	}
	if key, ok := message.(tea.KeyMsg); ok && key.String() == ":" {
		m.resourceMenu = false
		m.resourceSearch.Blur()
		m.commandMode = true
		m.commandInput.Reset()
		m.commandInput.SetSuggestions(resourceCompletionSuggestions(m.resources, ""))
		m.commandInput.Focus()
		m.status = "Command"
		return m, nil
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
		m.resourceSearch.Reset()
		m.resourceSearch.SetSuggestions(resourceCompletionSuggestions(m.resources, ""))
		m.resourceSearch.Focus()
		m.resourceIndex = resourceIndex(m.resources, m.resource.Key())
	case "up", "k":
		m.selected = clamp(m.selected-1, len(m.rows))
	case "down", "j":
		m.selected = clamp(m.selected+1, len(m.rows))
	case "enter":
		if len(m.rows) == 0 {
			return m, nil
		}
		m.err = nil
		m.loading = true
		m.objectID = m.rows[m.selected].ID
		m.status = "Loading object..."
		return m, m.loadObjectCmd(m.objectID)
	case "r":
		m.err = nil
		m.loading = true
		m.status = "Refreshing..."
		return m, m.loadRowsCmd()
	case "c":
		if !m.resource.Writable() {
			m.status = "Resource is read-only"
			return m, nil
		}
		m.err = nil
		m.action = "create"
		m.object = m.resource.New()
		data, err := yamlcodec.MarshalTemplate(m.object)
		if err != nil {
			m.err = err
			m.errorDialog = true
			return m, nil
		}
		command, err := m.editCmd(data)
		if err != nil {
			m.err = err
			m.errorDialog = true
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
				m.errorDialog = true
				return m, nil
			}
			m.action = "update"
			command, err := m.editCmd(data)
			if err != nil {
				m.err = err
				m.errorDialog = true
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
			m.err = nil
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
	case "esc":
		m.resourceMenu = false
		m.resourceSearch.Blur()
	case "tab":
		if m.resourceSearch.Value() == "" || len(m.resourceSearch.MatchedSuggestions()) == 0 {
			m.resourceMenu = false
			m.resourceSearch.Blur()
			return m, nil
		}
		var command tea.Cmd
		m.resourceSearch, command = m.resourceSearch.Update(message)
		m.refreshResourceSearch()
		return m, command
	case "up", "k":
		if key.String() == "up" && len(m.resourceSearch.MatchedSuggestions()) > 0 {
			var command tea.Cmd
			m.resourceSearch, command = m.resourceSearch.Update(message)
			m.refreshResourceSearch()
			return m, command
		}
		m.resourceIndex = clamp(m.resourceIndex-1, len(m.filteredResourceIndexes()))
	case "down", "j":
		if key.String() == "down" && len(m.resourceSearch.MatchedSuggestions()) > 0 {
			var command tea.Cmd
			m.resourceSearch, command = m.resourceSearch.Update(message)
			m.refreshResourceSearch()
			return m, command
		}
		m.resourceIndex = clamp(m.resourceIndex+1, len(m.filteredResourceIndexes()))
	case "enter":
		indexes := m.filteredResourceIndexes()
		if len(indexes) == 0 {
			return m, nil
		}
		m.resource = m.resources[indexes[clamp(m.resourceIndex, len(indexes))]]
		m.resourceMenu = false
		m.resourceSearch.Blur()
		m.selected = 0
		m.err = nil
		m.loading = true
		m.status = "Loading resources..."
		return m, m.loadRowsCmd()
	default:
		var command tea.Cmd
		m.resourceSearch, command = m.resourceSearch.Update(message)
		m.refreshResourceSearch()
		return m, command
	}
	return m, nil
}

func (m Model) updateCommand(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.commandMode = false
			m.commandInput.Blur()
			m.status = fmt.Sprintf("%d %s", len(m.rows), m.resource.Title())
			return m, nil
		case "enter":
			query := strings.TrimSpace(m.commandInput.Value())
			if suggestion := m.commandInput.CurrentSuggestion(); suggestion != "" {
				query = suggestion
			}
			m.commandMode = false
			m.commandInput.Blur()
			return m.selectResource(query)
		}
	}
	var command tea.Cmd
	m.commandInput, command = m.commandInput.Update(message)
	m.commandInput.SetSuggestions(resourceCompletionSuggestions(m.resources, m.commandInput.Value()))
	return m, command
}

func (m Model) selectResource(query string) (tea.Model, tea.Cmd) {
	if query == "" {
		m.status = "Resource name is required"
		return m, nil
	}
	indexes := matchingResourceIndexes(m.resources, query)
	if len(indexes) == 0 {
		m.status = "Unknown resource: " + query
		return m, nil
	}
	if len(indexes) > 1 {
		m.status = "Ambiguous resource: " + query
		return m, nil
	}
	m.resource = m.resources[indexes[0]]
	m.resourceMenu = false
	m.resourceSearch.Blur()
	m.screen = listScreen
	m.selected = 0
	m.err = nil
	m.loading = true
	m.status = "Loading resources..."
	return m, m.loadRowsCmd()
}

func normalizeResourceName(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func resourceCompletionSuggestions(resources []client.Resource, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		suggestions := make([]string, 0, len(resources))
		for _, resource := range resources {
			suggestions = append(suggestions, resource.Key())
		}
		return suggestions
	}

	suggestions := make([]string, 0, len(resources))
	for _, resource := range resources {
		key := resource.Key()
		title := resource.Title()
		candidate := ""
		if strings.HasPrefix(strings.ToLower(key), query) {
			candidate = key
		} else if strings.HasPrefix(strings.ToLower(title), query) {
			candidate = title
		}
		if candidate != "" {
			suggestions = append(suggestions, candidate)
		}
	}
	return suggestions
}

func (m Model) filteredResourceIndexes() []int {
	return matchingResourceIndexes(m.resources, m.resourceSearch.Value())
}

func (m *Model) refreshResourceSearch() {
	m.resourceSearch.SetSuggestions(resourceCompletionSuggestions(m.resources, m.resourceSearch.Value()))
	indexes := m.filteredResourceIndexes()
	if len(indexes) == 0 {
		m.resourceIndex = 0
		return
	}

	suggestion := normalizeResourceName(m.resourceSearch.CurrentSuggestion())
	if suggestion != "" {
		for position, index := range indexes {
			resource := m.resources[index]
			if normalizeResourceName(resource.Key()) == suggestion || normalizeResourceName(resource.Title()) == suggestion {
				m.resourceIndex = position
				return
			}
		}
	}
	m.resourceIndex = clamp(m.resourceIndex, len(indexes))
}

func matchingResourceIndexes(resources []client.Resource, query string) []int {
	normalized := normalizeResourceName(query)
	if normalized == "" {
		if strings.TrimSpace(query) != "" {
			return nil
		}
		indexes := make([]int, len(resources))
		for index := range resources {
			indexes[index] = index
		}
		return indexes
	}

	type match struct {
		index int
		rank  int
	}
	matches := make([]match, 0, len(resources))
	for index, resource := range resources {
		key := normalizeResourceName(resource.Key())
		title := normalizeResourceName(resource.Title())
		rank := -1
		if key == normalized || title == normalized {
			rank = 0
		} else if strings.HasPrefix(key, normalized) || strings.HasPrefix(title, normalized) {
			rank = 1
		} else if strings.Contains(key, normalized) || strings.Contains(title, normalized) {
			rank = 2
		}
		if rank >= 0 {
			matches = append(matches, match{index: index, rank: rank})
		}
	}
	sort.SliceStable(matches, func(left, right int) bool {
		return matches[left].rank < matches[right].rank
	})
	indexes := make([]int, len(matches))
	for index, match := range matches {
		indexes[index] = match.index
	}
	return indexes
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	m.viewport.Height = maxInt(m.bodyHeight()-2, 1)
	if m.errorDialog {
		return m.errorView()
	}
	switch m.screen {
	case listScreen:
		return m.listPage()
	case detailScreen:
		return m.detailPage()
	}
	panic("unknown screen")
}

func (m Model) errorView() string {
	width := minInt(maxInt(m.width-4, 1), 100)
	message := wrapText(m.err.Error(), width-4)
	body := lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("204")).Render("REQUEST FAILED"),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(message),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("enter or esc close"),
	)
	dialog := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("204")).
		Padding(1, 1).
		Render(body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) listPage() string {
	sections := []string{m.headerView(), m.bodyView()}
	if m.commandMode {
		sections = append(sections, m.commandView())
	}
	footer := m.footerView()
	sections = append(sections, footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) detailPage() string {
	sections := []string{m.headerView(), m.detailView()}
	if m.commandMode {
		sections = append(sections, m.commandView())
	}
	sections = append(sections, m.footerView())
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) headerView() string {
	osacVersion := m.osacVersion
	if osacVersion == "" {
		osacVersion = "unknown"
	}
	user := m.connection.User
	if m.connection.Organization != "" {
		user += " (" + m.connection.Organization + ")"
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render(
		truncate(fmt.Sprintf("OSAC TUI %s  /  %s", m.tuiVersion, m.resource.Title()), maxInt(m.width-6, 1)))
	context := fmt.Sprintf("%s  |  %s  |  OSAC %s", m.connection.Address, user, osacVersion)
	lines := []string{title, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		truncate(context, maxInt(m.width-6, 1)))}
	width := maxInt(m.width-2, 1)
	return lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (m Model) commandView() string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(0, 1).
		Render(m.commandInput.View())
}

func (m Model) bodyView() string {
	if !m.resourceMenu {
		return m.listView(m.width, m.bodyHeight())
	}

	menuWidth := minInt(42, maxInt(m.width/3, 24))
	if menuWidth >= m.width {
		menuWidth = maxInt(m.width/2, 1)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.resourceMenuView(menuWidth, m.bodyHeight()),
		m.listView(maxInt(m.width-menuWidth, 1), m.bodyHeight()),
	)
}

func (m Model) bodyHeight() int {
	overhead := 5
	if m.commandMode {
		overhead += 3
	}
	return maxInt(m.height-overhead, 1)
}

func (m Model) listView(width, height int) string {
	innerWidth := maxInt(width-4, 1)
	nameWidth, stateWidth, idWidth, versionWidth := columnWidths(innerWidth)
	line := truncate(tableLine(nameWidth, stateWidth, idWidth, versionWidth, "", "NAME", "STATE", "ID", "VERSION"), innerWidth)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("241")).Render(line)
	lines := []string{header}
	capacity := maxInt(height-3, 1)
	start, end := visibleRows(m.selected, len(m.rows), capacity)
	for index := start; index < end; index++ {
		row := m.rows[index]
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		cursor := "  "
		if index == m.selected {
			cursor = "> "
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))
		}
		lines = append(lines, style.Render(truncate(tableLine(
			nameWidth,
			stateWidth,
			idWidth,
			versionWidth,
			cursor,
			row.Name,
			compactState(row.State),
			row.ID,
			row.Version,
		), innerWidth)))
	}
	if len(m.rows) == 0 && !m.loading {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("No resources found"))
	}
	return panel(strings.Join(lines, "\n"), width, height, lipgloss.Color("99"))
}

func (m Model) resourceMenuView(width, height int) string {
	indexes := m.filteredResourceIndexes()
	innerHeight := maxInt(height-5, 1)
	start, end := visibleRows(m.resourceIndex, len(indexes), innerHeight)
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("RESOURCE KINDS"),
		m.resourceSearchView(width),
	}
	for position := start; position < end; position++ {
		resource := m.resources[indexes[position]]
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if position == m.resourceIndex {
			cursor = "> "
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))
		}
		lines = append(lines, style.Render(cursor+truncate(resource.Title(), maxInt(width-6, 1))))
	}
	if len(indexes) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("No matching kinds"))
	}
	matchCount := fmt.Sprintf("%d/%d kinds", len(indexes), len(m.resources))
	lines = append(lines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(
		matchCount+"  |  type to search  |  enter select"))
	return panel(strings.Join(lines, "\n"), width, height, lipgloss.Color("205"))
}

func (m Model) resourceSearchView(width int) string {
	search := m.resourceSearch
	if search.Prompt == "" {
		search = textinput.New()
		search.Prompt = "/"
	}
	search.Width = maxInt(width-6, 1)
	return search.View()
}

func (m Model) detailView() string {
	content := m.viewport.View()
	if m.confirmDelete {
		warning := lipgloss.NewStyle().Foreground(lipgloss.Color("204")).Bold(true).Render("Delete this object? [y/N]")
		content = lipgloss.JoinVertical(lipgloss.Left, warning, "", content)
	}
	return panel(content, m.width, m.bodyHeight(), lipgloss.Color("99"))
}

func (m Model) footerView() string {
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	if m.err != nil {
		statusStyle = statusStyle.Foreground(lipgloss.Color("204"))
	}
	statusText := truncate(m.status, maxInt(m.width/2, 1))
	status := statusStyle.Render(statusText)
	keyText := "tab kinds  : command  enter view  r refresh  q quit"
	if m.commandMode {
		keyText = "up/down cycle  tab complete  enter select  esc cancel"
	} else if m.resource.Writable() {
		keyText = "tab kinds  c create  enter view  e edit  d delete  r refresh  q quit"
	}
	keyWidth := maxInt(m.width-lipgloss.Width(statusText)-6, 1)
	keys := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(truncate(keyText, keyWidth))
	return lipgloss.NewStyle().Width(maxInt(m.width-2, 1)).Render(
		lipgloss.JoinHorizontal(lipgloss.Left, status, "    ", keys))
}

func panel(content string, width, height int, border lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().
		Width(maxInt(width-2, 1)).
		Height(maxInt(height, 1)).
		MaxHeight(maxInt(height, 1)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(content)
}

func tableLine(nameWidth, stateWidth, idWidth, versionWidth int, cursor, name, state, id, version string) string {
	return fmt.Sprintf(
		"%-2s %-*s %-*s %-*s %-*s",
		cursor,
		nameWidth,
		truncate(name, nameWidth),
		stateWidth,
		truncate(state, stateWidth),
		idWidth,
		truncate(id, idWidth),
		versionWidth,
		truncate(version, versionWidth),
	)
}

func columnWidths(width int) (int, int, int, int) {
	nameWidth := maxInt(width/4, 10)
	stateWidth := maxInt(width/6, 8)
	idWidth := maxInt(width/3, 12)
	versionWidth := maxInt(width-nameWidth-stateWidth-idWidth-4, 8)
	return nameWidth, stateWidth, idWidth, versionWidth
}

func compactState(state string) string {
	if separator := strings.LastIndex(state, "_"); separator >= 0 {
		return state[separator+1:]
	}
	return state
}

func truncate(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func wrapText(value string, width int) string {
	words := strings.Fields(value)
	lines := make([]string, 0, len(words))
	line := ""
	for _, word := range words {
		if line == "" {
			line = word
			continue
		}
		candidate := line + " " + word
		if lipgloss.Width(candidate) <= width {
			line = candidate
			continue
		}
		lines = append(lines, line)
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func minInt(value, maximum int) int {
	if value > maximum {
		return maximum
	}
	return value
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
