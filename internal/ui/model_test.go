package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/osac-project/osac-tui/internal/client"
	"google.golang.org/protobuf/proto"
)

type testResource struct {
	key   string
	title string
}

func (r testResource) Key() string { return r.key }

func (r testResource) Title() string {
	if r.title != "" {
		return r.title
	}
	return r.key
}

func (r testResource) List(context.Context, client.ListOptions) (client.ListResult, error) {
	return client.ListResult{}, nil
}

func (r testResource) Get(context.Context, string) (proto.Message, error) { return nil, nil }

func (r testResource) Create(context.Context, proto.Message) (proto.Message, error) { return nil, nil }

func (r testResource) Update(context.Context, proto.Message) (proto.Message, error) { return nil, nil }

func (r testResource) Delete(context.Context, string) error { return nil }

func (r testResource) New() proto.Message { return nil }

func (r testResource) Row(proto.Message) client.Row { return client.Row{} }

func (r testResource) Writable() bool { return false }

func TestViewFitsTerminalWithoutResourceMenu(t *testing.T) {
	for _, size := range []struct {
		width  int
		height int
	}{{40, 12}, {80, 24}, {100, 30}, {160, 40}} {
		model := Model{
			resource: testResource{key: "Projects"},
			width:    size.width,
			height:   size.height,
			status:   "Ready",
			screen:   listScreen,
		}

		view := model.View()
		if got := lipgloss.Height(view); got != model.height {
			t.Errorf("width %d height %d: view height = %d, want %d", model.width, model.height, got, model.height)
		}
		if got := lipgloss.Width(view); got > model.width {
			t.Errorf("width %d height %d: view width = %d, want at most %d", model.width, model.height, got, model.width)
		}
	}
}

func TestViewFitsTerminalWithResourceMenu(t *testing.T) {
	resources := make([]client.Resource, 20)
	for index := range resources {
		resources[index] = testResource{key: "Resource"}
	}
	for _, size := range []struct {
		width  int
		height int
	}{{40, 12}, {80, 24}, {100, 30}, {160, 40}} {
		model := Model{
			resource:      resources[0],
			resources:     resources,
			resourceMenu:  true,
			resourceIndex: 15,
			width:         size.width,
			height:        size.height,
			status:        "Ready",
			screen:        listScreen,
		}

		view := model.View()
		if got := lipgloss.Height(view); got != model.height {
			t.Errorf("width %d height %d: view height = %d, want %d", model.width, model.height, got, model.height)
		}
		if got := lipgloss.Width(view); got > model.width {
			t.Errorf("width %d height %d: view width = %d, want at most %d", model.width, model.height, got, model.width)
		}
	}
}

func TestTenantPickerViewFitsTerminal(t *testing.T) {
	rows := make([]client.Row, 30)
	for index := range rows {
		rows[index].Name = "tenant"
	}
	for _, size := range []struct {
		width  int
		height int
	}{{40, 12}, {80, 24}, {100, 30}} {
		model := Model{
			tenantPicker: true,
			tenantRows:   rows,
			tenantIndex:  17,
			width:        size.width,
			height:       size.height,
		}

		view := model.View()
		if got := lipgloss.Height(view); got != model.height {
			t.Errorf("width %d height %d: view height = %d, want %d", model.width, model.height, got, model.height)
		}
		if got := lipgloss.Width(view); got > model.width {
			t.Errorf("width %d height %d: view width = %d, want at most %d", model.width, model.height, got, model.width)
		}
	}
}

func TestTruncatePreservesValidUTF8(t *testing.T) {
	if got := truncate("東京", 1); got != "東" {
		t.Fatalf("truncate = %q, want %q", got, "東")
	}
}

func TestViewDoesNotRenderRawRPCError(t *testing.T) {
	model := Model{
		resource: testResource{key: "Bare Metal Instances"},
		width:    100,
		height:   30,
		status:   "Unavailable on this server",
		err:      errors.New("rpc error: code = Unavailable desc = service is not enabled"),
		screen:   listScreen,
	}

	view := model.View()
	if !strings.Contains(view, "No resources found") {
		t.Fatal("view does not contain the empty-state message")
	}
	if strings.Contains(view, "not enabled") {
		t.Fatal("view incorrectly claims the resource is disabled")
	}
	if strings.Contains(view, model.err.Error()) {
		t.Fatal("view contains the raw RPC error")
	}
}

func TestListFailureShowsRPCErrorDialog(t *testing.T) {
	rpcErr := errors.New("rpc error: code = Unauthenticated desc = token issuer is not trusted")
	model := Model{
		resource: testResource{key: "Projects"},
		width:    100,
		height:   30,
		loading:  true,
		screen:   listScreen,
	}

	updated, _ := model.Update(rowsLoadedMsg{err: rpcErr})
	model = updated.(Model)
	if !model.errorDialog {
		t.Fatal("list failure did not open the error dialog")
	}
	if !strings.Contains(model.View(), rpcErr.Error()) {
		t.Fatal("error dialog does not show the list RPC error")
	}
}

func TestErrorDialogShowsRawRPCError(t *testing.T) {
	model := Model{
		resource:    testResource{key: "Projects"},
		width:       100,
		height:      30,
		status:      "Unable to save object",
		err:         errors.New("rpc error: code = InvalidArgument desc = metadata.name is required"),
		errorDialog: true,
		screen:      listScreen,
	}

	view := model.View()
	if !strings.Contains(view, model.err.Error()) {
		t.Fatal("error dialog does not contain the RPC error")
	}
	if got := lipgloss.Height(view); got != model.height {
		t.Fatalf("dialog height = %d, want %d", got, model.height)
	}
	if got := lipgloss.Width(view); got > model.width {
		t.Fatalf("dialog width = %d, want at most %d", got, model.width)
	}
}

func TestResourceSearchMatchesKeysTitlesAndSubstrings(t *testing.T) {
	resources := []client.Resource{
		testResource{key: "projectmemberships", title: "Project Memberships"},
		testResource{key: "projects", title: "Projects"},
		testResource{key: "clustertemplates", title: "Cluster Templates"},
	}

	if got := matchingResourceIndexes(resources, "projects"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("exact resource match = %v, want [1]", got)
	}
	if got := matchingResourceIndexes(resources, "member"); len(got) != 1 || got[0] != 0 {
		t.Fatalf("substring resource match = %v, want [0]", got)
	}
	if got := matchingResourceIndexes(resources, "cluster temp"); len(got) != 1 || got[0] != 2 {
		t.Fatalf("title resource match = %v, want [2]", got)
	}
}

func TestSortAndPaginationControls(t *testing.T) {
	model := Model{page: 0, pageSize: 50, total: 105, sortField: "metadata.name"}
	if !model.hasNextPage() {
		t.Fatal("first page should have a next page")
	}
	model.page = 2
	if model.hasNextPage() {
		t.Fatal("last page should not have a next page")
	}

	model.nextSort()
	if model.sortOrder() != "metadata.name desc" {
		t.Fatalf("descending sort = %q", model.sortOrder())
	}
	model.nextSort()
	if model.sortOrder() != "status.state" {
		t.Fatalf("next sort field = %q", model.sortOrder())
	}
}

func TestSelectingTenantResetsListState(t *testing.T) {
	model := Model{
		resource:     testResource{key: "projects"},
		tenantRows:   []client.Row{{Name: "team-a"}},
		tenantPicker: true,
		page:         2,
		selected:     4,
		filter:       "old query",
		filterInput:  textinput.New(),
		selectedIDs:  map[string]bool{"project-1": true},
	}

	updated, command := model.selectTenantView(1)
	if command == nil {
		t.Fatal("tenant switch did not schedule a resource reload")
	}
	if updated.tenant != "team-a" {
		t.Errorf("tenant scope = %q, want team-a", updated.tenant)
	}
	if updated.page != 0 || updated.selected != 0 || updated.filter != "" || len(updated.selectedIDs) != 0 {
		t.Errorf("tenant switch did not reset list state: page=%d selected=%d filter=%q selectedIDs=%v",
			updated.page, updated.selected, updated.filter, updated.selectedIDs)
	}
}

func TestFuzzyMatch(t *testing.T) {
	if !fuzzyMatch("tenant1", "te1") {
		t.Fatal("expected fuzzy match")
	}
	if fuzzyMatch("tenant1", "tx") {
		t.Fatal("unexpected fuzzy match")
	}
}

func TestRejectedEditorDataPreservesDocumentAndAddsErrorComment(t *testing.T) {
	data := rejectedEditorData([]byte("metadata:\n  name: demo\n"), errors.New("metadata.name is invalid"))
	text := string(data)
	if !strings.Contains(text, "# Save rejected: metadata.name is invalid") {
		t.Fatalf("error comment missing:\n%s", text)
	}
	if !strings.Contains(text, "name: demo") {
		t.Fatalf("submitted document was not preserved:\n%s", text)
	}
}

func TestEntityAge(t *testing.T) {
	if got := entityAge(time.Now().Add(-2 * time.Hour)); got != "2h" {
		t.Fatalf("age = %q, want 2h", got)
	}
	if got := entityAge(time.Time{}); got != "-" {
		t.Fatalf("zero age = %q, want -", got)
	}
}

func TestConsoleKeyBytes(t *testing.T) {
	if got := string(consoleKeyBytes(tea.KeyMsg{Type: tea.KeyEnter})); got != "\r" {
		t.Fatalf("enter bytes = %q", got)
	}
	if got := string(consoleKeyBytes(tea.KeyMsg{Type: tea.KeyUp})); got != "\x1b[A" {
		t.Fatalf("up bytes = %q", got)
	}
}

func TestCommandCompletionCyclesAndSelectsSuggestion(t *testing.T) {
	resources := []client.Resource{
		testResource{key: "projectmemberships", title: "Project Memberships"},
		testResource{key: "projects", title: "Projects"},
	}
	input := textinput.New()
	input.Prompt = ":"
	input.ShowSuggestions = true
	input.SetSuggestions(resourceCompletionSuggestions(resources, ""))
	input.Focus()
	model := Model{resources: resources, resource: resources[0], commandInput: input, commandMode: true}

	updated, _ := model.updateCommand(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)
	if got := model.commandInput.CurrentSuggestion(); got != "projectmemberships" {
		t.Fatalf("first completion = %q, want projectmemberships", got)
	}

	updated, _ = model.updateCommand(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if got := model.commandInput.CurrentSuggestion(); got != "projects" {
		t.Fatalf("cycled completion = %q, want projects", got)
	}

	updated, _ = model.updateCommand(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if got := model.resource.Key(); got != "projects" {
		t.Fatalf("selected resource = %q, want projects", got)
	}
}

func TestTenantCommandIsNotReplacedByTenantsResourceSuggestion(t *testing.T) {
	input := textinput.New()
	input.Prompt = ":"
	input.ShowSuggestions = true
	input.SetValue("tenant")
	input.SetSuggestions([]string{"tenants", "tenant"})
	model := Model{
		resources:    []client.Resource{testResource{key: "tenants"}},
		resource:     testResource{key: "projects"},
		commandInput: input,
		commandMode:  true,
	}

	updated, _ := model.updateCommand(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.resource.Key() != "projects" {
		t.Fatalf("resource changed to %q instead of opening tenant picker", model.resource.Key())
	}
	if model.status != "Tenant selection is unavailable" {
		t.Fatalf("status = %q, want tenant picker command", model.status)
	}
}

func TestResourceMenuSearchCyclesAndSelectsSuggestion(t *testing.T) {
	resources := []client.Resource{
		testResource{key: "projectmemberships", title: "Project Memberships"},
		testResource{key: "projects", title: "Projects"},
	}
	search := textinput.New()
	search.Prompt = "/"
	search.ShowSuggestions = true
	search.SetSuggestions(resourceCompletionSuggestions(resources, ""))
	search.Focus()
	model := Model{
		resources:      resources,
		resource:       resources[0],
		resourceMenu:   true,
		resourceSearch: search,
	}

	updated, _ := model.updateResourceMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)
	if got := model.resourceSearch.CurrentSuggestion(); got != "projectmemberships" {
		t.Fatalf("first menu completion = %q, want projectmemberships", got)
	}

	updated, _ = model.updateResourceMenu(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if got := model.resourceSearch.CurrentSuggestion(); got != "projects" {
		t.Fatalf("cycled menu completion = %q, want projects", got)
	}

	updated, _ = model.updateResourceMenu(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if got := model.resource.Key(); got != "projects" {
		t.Fatalf("selected menu resource = %q, want projects", got)
	}
}
