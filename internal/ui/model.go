package ui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/osac-project/osac-tui/internal/client"
	"github.com/osac-project/osac-tui/internal/yamlcodec"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type screen uint8

const (
	listScreen screen = iota
	detailScreen
	consoleScreen
)

type rowsLoadedMsg struct {
	rows  []client.Row
	total int
	err   error
}

type autoRefreshMsg struct{}

type consoleOpenedMsg struct {
	console *client.SerialConsole
	err     error
}

type consoleOutputMsg struct {
	data   []byte
	status string
	err    error
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

type bulkDeleteFinishedMsg struct {
	deleted int
	err     error
}

type editorFinishedMsg struct {
	data      []byte
	unchanged bool
	err       error
}

type relatedReference struct {
	label    string
	resource client.Resource
	id       string
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
	api         *client.Client
	resource    client.Resource
	resources   []client.Resource
	connection  client.ConnectionInfo
	tuiVersion  string
	osacVersion string
	rows        []client.Row
	total       int
	selected    int
	selectedIDs map[string]bool
	page        int
	pageSize    int
	filter      string
	sortField   string
	sortDesc    bool

	screen        screen
	resourceMenu  bool
	resourceIndex int
	confirmDelete bool
	bulkDelete    bool
	loading       bool
	autoRefresh   bool

	object       proto.Message
	objectID     string
	action       string
	originalData []byte
	pendingData  []byte
	diffMode     bool

	viewport         viewport.Model
	commandInput     textinput.Model
	resourceSearch   textinput.Model
	filterInput      textinput.Model
	commandMode      bool
	filterMode       bool
	helpMode         bool
	relationshipMode bool
	references       []relatedReference
	referenceIndex   int
	console          *client.SerialConsole
	consoleText      string
	width            int
	height           int
	status           string
	err              error
	errorDialog      bool
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
	filterInput := textinput.New()
	filterInput.Prompt = "/"
	filterInput.CharLimit = 256
	model := Model{
		api:            api,
		resources:      resources,
		resource:       resources[0],
		connection:     api.ConnectionInfo(),
		tuiVersion:     tuiVersion,
		osacVersion:    osacVersion,
		viewport:       viewport.New(0, 0),
		commandInput:   commandInput,
		resourceSearch: resourceSearch,
		filterInput:    filterInput,
		selectedIDs:    make(map[string]bool),
		pageSize:       50,
		sortField:      "metadata.name",
		autoRefresh:    true,
		status:         "Loading resources...",
	}
	model.commandInput.SetSuggestions(resourceCompletionSuggestions(resources, ""))
	model.resourceSearch.SetSuggestions(resourceCompletionSuggestions(resources, ""))
	model.loading = true
	return model
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.loadRowsCmd(), m.autoRefreshCmd()) }

func (m Model) autoRefreshCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return autoRefreshMsg{} })
}

func (m Model) loadRowsCmd() tea.Cmd {
	resource := m.resource
	options := client.ListOptions{
		Offset: 0,
		Limit:  0,
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := resource.List(ctx, options)
		return rowsLoadedMsg{rows: result.Rows, total: result.Total, err: err}
	}
}

func (m Model) openSerialConsoleCmd() tea.Cmd {
	resourceType, ok := serialConsoleResourceType(m.resource.Key())
	if !ok || m.api == nil {
		return nil
	}
	resourceID := m.objectID
	api := m.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		console, err := api.OpenSerialConsole(ctx, resourceType, resourceID)
		return consoleOpenedMsg{console: console, err: err}
	}
}

func (m Model) receiveConsoleCmd() tea.Cmd {
	console := m.console
	if console == nil {
		return nil
	}
	return func() tea.Msg {
		data, status, err := console.Receive()
		return consoleOutputMsg{data: data, status: status, err: err}
	}
}

func serialConsoleResourceType(key string) (publicv1.ConsoleResourceType, bool) {
	switch key {
	case "computeinstances":
		return publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_COMPUTE_INSTANCE, true
	case "baremetalinstances":
		return publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_HOST, true
	default:
		return publicv1.ConsoleResourceType_CONSOLE_RESOURCE_TYPE_UNSPECIFIED, false
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
	originalData := append([]byte(nil), data...)
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
		return editorFinishedMsg{data: data, unchanged: bytes.Equal(data, originalData)}
	}), nil
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case autoRefreshMsg:
		if !m.autoRefresh || m.loading || m.diffMode || m.filterMode || m.commandMode || m.screen == consoleScreen {
			if m.autoRefresh {
				return m, m.autoRefreshCmd()
			}
			return m, nil
		}
		m.loading = true
		m.status = "Auto-refreshing..."
		return m, tea.Batch(m.loadRowsCmd(), m.autoRefreshCmd())
	case consoleOpenedMsg:
		if message.err != nil {
			m.err = message.err
			m.errorDialog = true
			m.status = "Unable to open serial console"
			return m, nil
		}
		m.console = message.console
		m.consoleText = ""
		m.screen = consoleScreen
		m.status = "Serial console connected; esc closes"
		return m, m.receiveConsoleCmd()
	case consoleOutputMsg:
		if message.err != nil {
			m.status = "Serial console disconnected: " + message.err.Error()
			if m.console != nil {
				_ = m.console.Close()
			}
			m.console = nil
			return m, nil
		}
		if message.status != "" {
			m.status = message.status
		}
		m.consoleText += string(message.data)
		m.viewport.SetContent(m.consoleText)
		m.viewport.GotoBottom()
		return m, m.receiveConsoleCmd()
	case consoleInputMsg:
		if message.err != nil {
			m.status = "Console input failed: " + message.err.Error()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		m.viewport.Width = maxInt(message.Width-6, 1)
		m.viewport.Height = m.bodyHeight()
		m.commandInput.Width = maxInt(message.Width-8, 1)
		m.resourceSearch.Width = maxInt(message.Width/3-6, 1)
		m.filterInput.Width = maxInt(message.Width-8, 1)
		return m, nil
	case rowsLoadedMsg:
		m.loading = false
		rows := message.rows
		if query := strings.TrimSpace(m.filter); query != "" {
			rows = fuzzyRows(rows, query)
			m.total = len(rows)
		} else {
			m.total = message.total
		}
		sortRows(rows, m.sortField, m.sortDesc)
		start := minInt(m.page*m.effectivePageSize(), len(rows))
		end := minInt(start+m.effectivePageSize(), len(rows))
		m.rows = rows[start:end]
		m.selected = clamp(m.selected, len(m.rows))
		m.err = message.err
		if message.err != nil {
			m.status = "Unavailable on this server"
			return m, nil
		}
		m.err = nil
		m.status = fmt.Sprintf("%d/%d %s", len(m.rows), m.total, m.resource.Title())
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
		m.viewport.SetContent(string(yamlcodec.RedactSensitive(message.data)))
		m.viewport.GotoTop()
		m.screen = detailScreen
		m.status = "Read-only view"
		return m, nil
	case mutationFinishedMsg:
		m.loading = false
		m.err = message.err
		if message.err != nil {
			data := rejectedEditorData(m.pendingData, message.err)
			command, err := m.editCmd(data)
			if err != nil {
				m.err = err
				m.errorDialog = true
				m.status = "Editor failed"
				return m, nil
			}
			m.err = message.err
			m.status = "Save rejected; edit and resubmit"
			return m, command
		}
		m.err = nil
		m.object = message.object
		m.objectID = m.resource.Row(message.object).ID
		m.viewport.SetContent(string(yamlcodec.RedactSensitive(message.data)))
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
	case bulkDeleteFinishedMsg:
		m.loading = false
		m.bulkDelete = false
		m.selectedIDs = make(map[string]bool)
		if message.err != nil {
			m.err = message.err
			m.errorDialog = true
			m.status = fmt.Sprintf("Deleted %d objects; some deletions failed", message.deleted)
			return m, nil
		}
		m.err = nil
		m.status = fmt.Sprintf("Deleted %d objects", message.deleted)
		return m, m.loadRowsCmd()
	case editorFinishedMsg:
		if message.err != nil {
			m.err = nil
			m.errorDialog = false
			m.pendingData = nil
			m.diffMode = false
			m.status = "Editor canceled"
			return m, nil
		}
		if message.unchanged {
			m.pendingData = nil
			m.diffMode = false
			m.status = "Editor canceled; no changes saved"
			return m, nil
		}
		m.err = nil
		object := m.resource.New()
		if err := yamlcodec.Unmarshal(message.data, object); err != nil {
			data := rejectedEditorData(message.data, err)
			command, editorErr := m.editCmd(data)
			if editorErr != nil {
				m.err = editorErr
				m.errorDialog = true
				m.status = "Editor failed"
				return m, nil
			}
			m.err = err
			m.status = "Invalid YAML; edit and resubmit"
			return m, command
		}
		m.object = object
		m.pendingData = append([]byte(nil), message.data...)
		if m.action == "update" && string(m.originalData) != string(message.data) {
			m.diffMode = true
			m.status = "Review changes [y/n]"
			return m, nil
		}
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
	if m.helpMode {
		return m.updateHelp(message)
	}
	if m.relationshipMode {
		return m.updateRelationships(message)
	}
	if m.screen == consoleScreen {
		return m.updateConsole(message)
	}
	if m.bulkDelete {
		return m.updateBulkDelete(message)
	}
	if m.diffMode {
		return m.updateDiff(message)
	}
	if m.commandMode {
		return m.updateCommand(message)
	}
	if m.filterMode {
		return m.updateFilter(message)
	}
	if key, ok := message.(tea.KeyMsg); ok && key.String() == "?" {
		m.helpMode = true
		return m, nil
	}
	if key, ok := message.(tea.KeyMsg); ok && key.String() == ":" {
		m.resourceMenu = false
		m.resourceSearch.Blur()
		m.commandMode = true
		m.commandInput.Reset()
		m.commandInput.SetSuggestions(commandSuggestions(m.resources, ""))
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
	case "?":
		m.helpMode = true
		return m, nil
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
	case " ":
		if len(m.rows) > 0 && m.rows[m.selected].ID != "" {
			if m.selectedIDs == nil {
				m.selectedIDs = make(map[string]bool)
			}
			id := m.rows[m.selected].ID
			m.selectedIDs[id] = !m.selectedIDs[id]
			m.selected = clamp(m.selected+1, len(m.rows))
		}
	case "d":
		if !m.resource.Writable() {
			m.status = "Resource is read-only"
			return m, nil
		}
		if len(selectedIDList(m.selectedIDs)) == 0 {
			m.status = "Select objects with space first"
			return m, nil
		}
		m.bulkDelete = true
		m.status = "Confirm bulk delete"
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
	case "a":
		m.autoRefresh = !m.autoRefresh
		if m.autoRefresh {
			m.status = "Auto-refresh enabled (1s)"
			return m, m.autoRefreshCmd()
		}
		m.status = "Auto-refresh disabled"
	case "/":
		m.filterMode = true
		m.filterInput.SetValue(m.filter)
		m.filterInput.CursorEnd()
		m.filterInput.Focus()
		m.status = "Filter"
	case "s":
		m.nextSort()
		m.loading = true
		m.status = "Sorting..."
		return m, m.loadRowsCmd()
	case "n":
		if m.hasNextPage() {
			m.page++
			m.selected = 0
			m.loading = true
			m.status = "Loading next page..."
			return m, m.loadRowsCmd()
		}
	case "p":
		if m.page > 0 {
			m.page--
			m.selected = 0
			m.loading = true
			m.status = "Loading previous page..."
			return m, m.loadRowsCmd()
		}
	case "c":
		if !m.resource.Writable() {
			m.status = "Resource is read-only"
			return m, nil
		}
		m.err = nil
		m.action = "create"
		m.originalData = nil
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
			m.status = fmt.Sprintf("%d/%d %s", len(m.rows), m.total, m.resource.Title())
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
			m.originalData = append([]byte(nil), data...)
			command, err := m.editCmd(data)
			if err != nil {
				m.err = err
				m.errorDialog = true
				m.status = "Editor failed"
				return m, nil
			}
			m.status = "Opening editor..."
			return m, command
		case "c":
			if _, ok := serialConsoleResourceType(m.resource.Key()); !ok {
				m.status = "Serial console is not supported for this resource"
				return m, nil
			}
			m.status = "Opening serial console..."
			return m, m.openSerialConsoleCmd()
		case "l":
			m.references = findRelatedReferences(m.object, m.resources)
			if len(m.references) == 0 {
				m.status = "No related resources found"
				return m, nil
			}
			m.relationshipMode = true
			m.referenceIndex = 0
			return m, nil
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
		case "y":
			data, err := yamlcodec.Marshal(m.object)
			if err != nil {
				m.err = err
				m.status = "Unable to copy YAML"
				return m, nil
			}
			if err := clipboard.WriteAll(string(data)); err != nil {
				m.err = err
				m.status = "Unable to copy YAML"
				return m, nil
			}
			m.err = nil
			m.status = "YAML copied to clipboard"
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
		m.page = 0
		m.filter = ""
		m.filterInput.Reset()
		m.selectedIDs = make(map[string]bool)
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
			m.status = fmt.Sprintf("%d/%d %s", len(m.rows), m.total, m.resource.Title())
			return m, nil
		case "enter":
			query := strings.TrimSpace(m.commandInput.Value())
			if suggestion := m.commandInput.CurrentSuggestion(); suggestion != "" {
				query = suggestion
			}
			m.commandMode = false
			m.commandInput.Blur()
			if updated, command, handled := m.executeCommand(query); handled {
				return updated, command
			}
			return m.selectResource(query)
		}
	}
	var command tea.Cmd
	m.commandInput, command = m.commandInput.Update(message)
	m.commandInput.SetSuggestions(commandSuggestions(m.resources, m.commandInput.Value()))
	return m, command
}

func (m Model) executeCommand(query string) (tea.Model, tea.Cmd, bool) {
	switch strings.ToLower(strings.TrimSpace(query)) {
	case "help":
		m.helpMode = true
		return m, nil, true
	case "filter":
		m.filterMode = true
		m.filterInput.SetValue(m.filter)
		m.filterInput.CursorEnd()
		m.filterInput.Focus()
		m.status = "Filter"
		return m, nil, true
	case "sort":
		m.nextSort()
		m.loading = true
		m.status = "Sorting..."
		return m, m.loadRowsCmd(), true
	case "refresh":
		m.loading = true
		m.status = "Refreshing..."
		return m, m.loadRowsCmd(), true
	case "next":
		if m.hasNextPage() {
			m.page++
			m.selected = 0
			m.loading = true
			m.status = "Loading next page..."
			return m, m.loadRowsCmd(), true
		}
		return m, nil, true
	case "previous":
		if m.page > 0 {
			m.page--
			m.selected = 0
			m.loading = true
			m.status = "Loading previous page..."
			return m, m.loadRowsCmd(), true
		}
		return m, nil, true
	case "quit":
		return m, tea.Quit, true
	default:
		return m, nil, false
	}
}

func (m Model) updateHelp(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "?", "esc", "q":
			m.helpMode = false
		}
	}
	return m, nil
}

func (m Model) updateRelationships(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc", "l":
		m.relationshipMode = false
	case "up", "k":
		m.referenceIndex = clamp(m.referenceIndex-1, len(m.references))
	case "down", "j":
		m.referenceIndex = clamp(m.referenceIndex+1, len(m.references))
	case "enter":
		if len(m.references) == 0 {
			return m, nil
		}
		reference := m.references[m.referenceIndex]
		m.resource = reference.resource
		m.objectID = reference.id
		m.relationshipMode = false
		m.loading = true
		m.status = "Loading related object..."
		return m, m.loadObjectCmd(reference.id)
	}
	return m, nil
}

func (m Model) updateConsole(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok || m.console == nil {
		return m, nil
	}
	if key.String() == "esc" || key.String() == "ctrl+c" {
		_ = m.console.Close()
		m.console = nil
		m.screen = detailScreen
		m.status = "Serial console closed"
		return m, nil
	}
	data := consoleKeyBytes(key)
	if len(data) == 0 {
		return m, nil
	}
	console := m.console
	return m, func() tea.Msg {
		return consoleInputMsg{err: console.Send(data)}
	}
}

func (m Model) updateBulkDelete(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y":
		ids := selectedIDList(m.selectedIDs)
		m.loading = true
		m.status = fmt.Sprintf("Deleting %d objects...", len(ids))
		return m, m.bulkDeleteCmd(ids)
	case "n", "esc":
		m.bulkDelete = false
		m.status = "Bulk delete canceled"
	}
	return m, nil
}

func (m Model) bulkDeleteCmd(ids []string) tea.Cmd {
	resource := m.resource
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		deleted := 0
		var failures []string
		for _, id := range ids {
			if err := resource.Delete(ctx, id); err != nil {
				failures = append(failures, id+": "+err.Error())
				continue
			}
			deleted++
		}
		if len(failures) > 0 {
			return bulkDeleteFinishedMsg{deleted: deleted, err: fmt.Errorf("%s", strings.Join(failures, "; "))}
		}
		return bulkDeleteFinishedMsg{deleted: deleted}
	}
}

func selectedIDs(rows []client.Row, selected map[string]bool) []string {
	ids := make([]string, 0, len(selected))
	for _, row := range rows {
		if selected[row.ID] {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

func selectedIDList(selected map[string]bool) []string {
	ids := make([]string, 0, len(selected))
	for id, isSelected := range selected {
		if isSelected {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

type consoleInputMsg struct{ err error }

func consoleKeyBytes(key tea.KeyMsg) []byte {
	if len(key.Runes) > 0 {
		return []byte(string(key.Runes))
	}
	switch key.String() {
	case "enter":
		return []byte("\r")
	case "backspace":
		return []byte("\x7f")
	case "tab":
		return []byte("\t")
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "ctrl+d":
		return []byte("\x04")
	case "ctrl+l":
		return []byte("\x0c")
	default:
		return nil
	}
}

func findRelatedReferences(object proto.Message, resources []client.Resource) []relatedReference {
	if object == nil {
		return nil
	}
	var result []relatedReference
	var walk func(protoreflect.Message, string, int)
	walk = func(message protoreflect.Message, path string, depth int) {
		if message == nil || depth > 3 {
			return
		}
		fields := message.Descriptor().Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			name := string(field.Name())
			if field.Kind() == protoreflect.StringKind && strings.HasSuffix(name, "_id") {
				id := message.Get(field).String()
				if id == "" {
					continue
				}
				if resource, ok := relatedResource(resources, strings.TrimSuffix(name, "_id")); ok {
					label := name
					if path != "" {
						label = path + "." + name
					}
					result = append(result, relatedReference{label: label, resource: resource, id: id})
				}
				continue
			}
			if field.Kind() == protoreflect.MessageKind && field.Cardinality() != protoreflect.Repeated && message.Has(field) {
				childPath := name
				if path != "" {
					childPath = path + "." + name
				}
				walk(message.Get(field).Message(), childPath, depth+1)
			}
		}
	}
	walk(object.ProtoReflect(), "", 0)
	return result
}

func relatedResource(resources []client.Resource, name string) (client.Resource, bool) {
	wanted := normalizeResourceName(name)
	for _, resource := range resources {
		key := normalizeResourceName(resource.Key())
		if key == wanted || strings.TrimSuffix(key, "s") == wanted {
			return resource, true
		}
	}
	return nil, false
}

func commandSuggestions(resources []client.Resource, query string) []string {
	commands := []string{"help", "filter", "sort", "refresh", "next", "previous", "quit"}
	suggestions := resourceCompletionSuggestions(resources, query)
	query = strings.ToLower(strings.TrimSpace(query))
	for _, command := range commands {
		if query == "" || strings.HasPrefix(command, query) {
			suggestions = append(suggestions, command)
		}
	}
	return suggestions
}

func (m Model) updateFilter(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			wasFiltered := strings.TrimSpace(m.filter) != ""
			m.filter = ""
			m.filterInput.Reset()
			m.filterMode = false
			m.filterInput.Blur()
			m.screen = listScreen
			m.page = 0
			m.selected = 0
			if wasFiltered {
				m.loading = true
				m.status = "Clearing filter..."
				return m, m.loadRowsCmd()
			}
			return m, nil
		case "enter":
			m.filter = strings.TrimSpace(m.filterInput.Value())
			m.filterMode = false
			m.filterInput.Blur()
			m.page = 0
			m.selected = 0
			m.loading = true
			m.status = "Applying filter..."
			return m, m.loadRowsCmd()
		}
	}
	var command tea.Cmd
	m.filterInput, command = m.filterInput.Update(message)
	return m, command
}

func (m Model) updateDiff(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "enter":
		m.diffMode = false
		m.loading = true
		m.status = actionProgress[m.action]
		return m, m.mutationCmd(m.object)
	case "n", "esc":
		m.diffMode = false
		m.status = "Changes discarded"
		return m, nil
	}
	return m, nil
}

func rejectedEditorData(data []byte, err error) []byte {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	comment := strings.Split(err.Error(), "\n")
	prefix := make([]string, 0, len(comment)+1)
	for _, line := range comment {
		prefix = append(prefix, "# Save rejected: "+line)
	}
	prefix = append(prefix, "# Edit the document and save again.")
	return []byte(strings.Join(append(prefix, lines...), "\n") + "\n")
}

func (m Model) sortOrder() string {
	if m.sortField == "" {
		return ""
	}
	order := m.sortField
	if m.sortDesc {
		order += " desc"
	}
	return order
}

func (m *Model) nextSort() {
	fields := []string{"metadata.name", "status.state", "metadata.tenant", "metadata.creation_timestamp"}
	for index, field := range fields {
		if m.sortField != field {
			continue
		}
		if !m.sortDesc {
			m.sortDesc = true
		} else {
			m.sortField = fields[(index+1)%len(fields)]
			m.sortDesc = false
		}
		return
	}
	m.sortField = fields[0]
	m.sortDesc = false
}

func (m Model) hasNextPage() bool {
	return (m.page+1)*m.effectivePageSize() < m.total
}

func fuzzyRows(rows []client.Row, query string) []client.Row {
	filtered := make([]client.Row, 0, len(rows))
	query = strings.ToLower(strings.TrimSpace(query))
	for _, row := range rows {
		candidate := strings.ToLower(strings.Join([]string{row.Name, row.ID, row.Tenant, row.State}, " "))
		if fuzzyMatch(candidate, query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func fuzzyMatch(value, query string) bool {
	if query == "" {
		return true
	}
	queryRunes := []rune(query)
	valueRunes := []rune(value)
	queryIndex := 0
	for _, character := range valueRunes {
		if character == queryRunes[queryIndex] {
			queryIndex++
			if queryIndex == len(queryRunes) {
				return true
			}
		}
	}
	return false
}

func sortRows(rows []client.Row, field string, descending bool) {
	sort.SliceStable(rows, func(left, right int) bool {
		leftValue := rowSortValue(rows[left], field)
		rightValue := rowSortValue(rows[right], field)
		if descending {
			return leftValue > rightValue
		}
		return leftValue < rightValue
	})
}

func rowSortValue(row client.Row, field string) string {
	switch field {
	case "status.state":
		return row.State
	case "metadata.tenant":
		return row.Tenant
	case "metadata.creation_timestamp":
		return row.Created.Format(time.RFC3339Nano)
	default:
		return row.Name
	}
}

func (m Model) effectivePageSize() int { return maxInt(m.pageSize, 50) }

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
	m.page = 0
	m.filter = ""
	m.filterInput.Reset()
	m.selectedIDs = make(map[string]bool)
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
	if m.helpMode {
		return m.helpView()
	}
	if m.relationshipMode {
		return m.relationshipView()
	}
	if m.bulkDelete {
		return m.bulkDeleteView()
	}
	if m.diffMode {
		return m.diffView()
	}
	switch m.screen {
	case listScreen:
		return m.listPage()
	case detailScreen:
		return m.detailPage()
	case consoleScreen:
		return m.consolePage()
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

func (m Model) helpView() string {
	content := strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("OSAC TUI HELP"),
		"",
		"Navigation",
		"  j/k or arrows     move through rows",
		"  enter             open selected object",
		"  space             select a row",
		"  d                 delete selected rows",
		"  l                 show related resources",
		"  tab               choose resource kind",
		"  n / p             next / previous page",
		"",
		"List actions",
		"  /                 fuzzy filter",
		"  s                 cycle sort field and direction",
		"  r                 refresh",
		"  a                 toggle 1-second auto-refresh",
		"  c / e / d         create / edit / delete",
		"  c (detail)        open serial console for instances",
		"  y (detail)        copy object YAML",
		"",
		"Commands",
		"  :                 open command palette",
		"  :filter           edit fuzzy filter",
		"  :sort             change sort",
		"  :refresh          reload resources",
		"",
		"? / esc             close help",
	}, "\n")
	width := minInt(maxInt(m.width-4, 1), 76)
	dialog := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) relationshipView() string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("RELATED RESOURCES"),
		"",
	}
	for index, reference := range m.references {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		if index == m.referenceIndex {
			cursor = "> "
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%-24s %s/%s", cursor, reference.label, reference.resource.Title(), reference.id)))
	}
	lines = append(lines, "", "up/down select  enter open  esc close")
	dialog := lipgloss.NewStyle().
		Width(minInt(maxInt(m.width-4, 1), 100)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) bulkDeleteView() string {
	count := len(selectedIDList(m.selectedIDs))
	body := fmt.Sprintf("Delete %d selected objects?\n\n[y] delete  [n/esc] cancel", count)
	dialog := lipgloss.NewStyle().
		Width(minInt(maxInt(m.width-4, 1), 60)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("204")).
		Padding(1, 2).
		Render(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("204")).Render("BULK DELETE") + "\n\n" + body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func (m Model) diffView() string {
	content := strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("REVIEW CHANGES"),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("204")).Render(yamlDiff(m.originalData, m.pendingData)),
		"",
		"submit changes? [y/N]",
	}, "\n")
	dialog := lipgloss.NewStyle().
		Width(minInt(maxInt(m.width-4, 1), 110)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, dialog)
}

func yamlDiff(before, after []byte) string {
	oldLines := strings.Split(strings.TrimSuffix(string(before), "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(string(after), "\n"), "\n")
	lines := []string{"--- current", "+++ submitted"}
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	for _, line := range oldLines[prefix : len(oldLines)-suffix] {
		if line != "" {
			lines = append(lines, "-"+line)
		}
	}
	for _, line := range newLines[prefix : len(newLines)-suffix] {
		if line != "" {
			lines = append(lines, "+"+line)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) listPage() string {
	sections := []string{m.headerView(), m.bodyView()}
	if m.commandMode {
		sections = append(sections, m.commandView())
	}
	if m.filterMode {
		sections = append(sections, m.filterView())
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
	if m.filterMode {
		sections = append(sections, m.filterView())
	}
	sections = append(sections, m.footerView())
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m Model) consolePage() string {
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(),
		panel(m.viewport.View(), m.width, m.bodyHeight(), lipgloss.Color("205")),
		m.footerView())
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

func (m Model) filterView() string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(0, 1).
		Render(m.filterInput.View())
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
	if m.filterMode {
		overhead += 3
	}
	return maxInt(m.height-overhead, 1)
}

func (m Model) listView(width, height int) string {
	innerWidth := maxInt(width-4, 1)
	nameWidth, stateWidth, tenantWidth, ageWidth, idWidth := columnWidths(innerWidth)
	line := truncate(tableLine(nameWidth, stateWidth, tenantWidth, ageWidth, idWidth, "",
		m.sortHeader("NAME", "metadata.name"),
		m.sortHeader("STATE", "status.state"),
		m.sortHeader("TENANT", "metadata.tenant"),
		m.sortHeader("AGE", "metadata.creation_timestamp"),
		"ID"), innerWidth)
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("241")).Render(line)
	lines := []string{header}
	capacity := maxInt(height-3, 1)
	start, end := visibleRows(m.selected, len(m.rows), capacity)
	for index := start; index < end; index++ {
		row := m.rows[index]
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
		cursor := "  "
		if m.selectedIDs[row.ID] {
			cursor = "* "
		}
		if index == m.selected {
			cursor = "> "
			if m.selectedIDs[row.ID] {
				cursor = ">*"
			}
			style = style.Background(lipgloss.Color("238")).Foreground(lipgloss.Color("230"))
		}
		lines = append(lines, style.Render(truncate(tableLine(
			nameWidth,
			stateWidth,
			tenantWidth,
			ageWidth,
			idWidth,
			cursor,
			row.Name,
			compactState(row.State),
			row.Tenant,
			entityAge(row.Created),
			row.ID,
		), innerWidth)))
	}
	if len(m.rows) == 0 && !m.loading {
		message := "No resources found"
		if m.filter != "" {
			message = "No resources match the filter"
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(message))
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
	pageSize := m.effectivePageSize()
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
	if m.err != nil {
		statusStyle = statusStyle.Foreground(lipgloss.Color("204"))
	}
	statusText := truncate(m.status, maxInt(m.width/2, 1))
	status := statusStyle.Render(statusText)
	keyText := fmt.Sprintf("tab kinds  / filter  space select  s sort  a auto-refresh  n/p page  enter view  q quit  page %d/%d",
		m.page+1, maxInt((m.total+pageSize-1)/pageSize, 1))
	if m.commandMode {
		keyText = "up/down cycle  tab complete  enter select  esc cancel"
	} else if m.filterMode {
		keyText = "enter apply  esc cancel  | fuzzy search"
	} else if m.screen == consoleScreen {
		keyText = "type to send input  esc close console"
	} else if m.resource.Writable() {
		keyText = fmt.Sprintf("tab kinds  / filter  space select  d bulk delete  s sort  a auto-refresh  n/p page  c create  enter view  e edit  r refresh  q quit  page %d/%d",
			m.page+1, maxInt((m.total+pageSize-1)/pageSize, 1))
	}
	keyWidth := maxInt(m.width-lipgloss.Width(statusText)-6, 1)
	keys := lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(truncate(keyText, keyWidth))
	return lipgloss.NewStyle().Width(maxInt(m.width-2, 1)).Render(
		lipgloss.JoinHorizontal(lipgloss.Left, status, "    ", keys))
}

func (m Model) sortHeader(label, field string) string {
	if m.sortField != field {
		return label
	}
	if m.sortDesc {
		return label + " v"
	}
	return label + " ^"
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

func tableLine(nameWidth, stateWidth, tenantWidth, ageWidth, idWidth int, cursor, name, state, tenant, age, id string) string {
	return fmt.Sprintf(
		"%-2s %-*s %-*s %-*s %-*s %-*s",
		cursor,
		nameWidth,
		truncate(name, nameWidth),
		stateWidth,
		truncate(state, stateWidth),
		tenantWidth,
		truncate(tenant, tenantWidth),
		ageWidth,
		truncate(age, ageWidth),
		idWidth,
		truncate(id, idWidth),
	)
}

func columnWidths(width int) (int, int, int, int, int) {
	nameWidth := maxInt(width/5, 10)
	stateWidth := maxInt(width/7, 8)
	tenantWidth := maxInt(width/7, 8)
	ageWidth := 6
	idWidth := maxInt(width-nameWidth-stateWidth-tenantWidth-ageWidth-5, 12)
	return nameWidth, stateWidth, tenantWidth, ageWidth, idWidth
}

func entityAge(created time.Time) string {
	if created.IsZero() {
		return "-"
	}
	age := time.Since(created)
	if age < 0 {
		return "0s"
	}
	if age >= 365*24*time.Hour {
		return fmt.Sprintf("%dy", int(age/(365*24*time.Hour)))
	}
	if age >= 30*24*time.Hour {
		return fmt.Sprintf("%dmo", int(age/(30*24*time.Hour)))
	}
	if age >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	}
	if age >= time.Hour {
		return fmt.Sprintf("%dh", int(age/time.Hour))
	}
	if age >= time.Minute {
		return fmt.Sprintf("%dm", int(age/time.Minute))
	}
	return fmt.Sprintf("%ds", int(age/time.Second))
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
