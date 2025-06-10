IMG=ghcr.io/doodlescheduling/flux-build

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

rwildcard=$(foreach d,$(wildcard $(addsuffix *,$(1))),$(call rwildcard,$(d)/,$(2)) $(filter $(subst *,%,$(2)),$(d)))

all: lint test build

tidy:
	go mod tidy -compat=1.22

fmt:
	go fmt ./...

.PHONY: test
test:
	go test -race -coverprofile coverage.out -v ./...

.PHONY: e2e-test
e2e-test: build
	./flux-build test/e2e/HelmRepository/overlay test/e2e/HelmRepository/repositories | yq ea '[.] | sort_by(.kind) | .[] | splitDoc' > build.yaml
	cmp test/e2e/HelmRepository/expected.yaml build.yaml && echo "HelmRepository e2e test passed"
	rm build.yaml
	./flux-build test/e2e/GitRepository/overlay test/e2e/GitRepository/repositories | yq ea '[.] | sort_by(.kind) | .[] | splitDoc' > build.yaml
	cmp test/e2e/GitRepository/expected.yaml build.yaml && echo "GitRepository e2e test passed"
	rm build.yaml

GOLANGCI_LINT = $(GOBIN)/golangci-lint
golangci-lint: ## Download golint locally if necessary.
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint@v1.52.2)

lint: golangci-lint
	golangci-lint run --timeout=3m

vet:
	go vet ./...

code-gen:
	./hack/code-gen.sh

build:
	CGO_ENABLED=0 go build -o ./flux-build .

.PHONY: docker-build
docker-build: build
	docker build -t ${IMG} .

.PHONY: install
install:
	CGO_ENABLED=0 go install .

# go-install-tool will 'go install' any package $2 and install it to $1
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
env -i bash -c "GOBIN=$(GOBIN) PATH=$(PATH) GOPATH=$(shell go env GOPATH) GOCACHE=$(shell go env GOCACHE) go install $(2)" ;\
rm -rf $$TMP_DIR ;\
}
endef
