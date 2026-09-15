GO ?= go
BINARY ?= osac-tui
KUBECONFIG ?= /home/rgolan/.kube/osac-dev-kind-root.kubeconfig

.PHONY: build test fmt run kind clean

build:
	$(GO) build -o $(BINARY) ./cmd/osac-tui

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
