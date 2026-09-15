package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/osac-project/osac-tui/internal/client"
	"google.golang.org/protobuf/proto"
)

type testResource struct {
	key string
}

func (r testResource) Key() string { return r.key }

func (r testResource) Title() string { return r.key }

func (r testResource) List(context.Context) ([]client.Row, error) { return nil, nil }

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
