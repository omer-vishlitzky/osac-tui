GO ?= go
BINARY ?= osac-tui
KUBECONFIG ?= /home/rgolan/.kube/osac-dev-kind-root.kubeconfig
VERSION ?= dev
LDFLAGS ?= -X github.com/osac-project/osac-tui/internal/version.Value=$(VERSION)

.PHONY: build test fmt run kind clean

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/osac-tui

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

run:
	$(GO) run ./cmd/osac-tui $(ARGS)

kind:
	KUBECONFIG=$(KUBECONFIG) bash ./run-kind.sh $(ARGS)

clean:
	rm -f $(BINARY)
