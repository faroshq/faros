SHELL = /bin/bash
OUTPUT_BIN_CLI ?= release/cli
OUTPUT_BIN_FAROS ?= release/faros
FAROS_REPO ?= quay.io/faroshq/faros
FAROS_CLI_REPO ?= quay.io/faroshq/faros-cli
TAG_NAME ?= $(shell git describe --tags --abbrev=0)
GIT_REVISION = $(shell git rev-parse --short HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
JOBDATE		?= $(shell date -u +%Y-%m-%dT%H%M%SZ)

LDFLAGS		+= -s -w
LDFLAGS     += -extldflags=-static
LDFLAGS		+= -X github.com/faroshq/faros/pkg/util/version.version=$(TAG_NAME)
LDFLAGS		+= -X github.com/faroshq/faros/pkg/util/version.commit=$(GIT_REVISION)
LDFLAGS		+= -X github.com/faroshq/faros/pkg/util/version.buildTime=$(JOBDATE)

run-server:
	go run  ./cmd/faros --loglevel=trace

run-agent:
	go run  ./cmd/agent --loglevel=trace


run-compose:
	docker-compose -f docker-compose-dev.yaml up --build faros

generate:
	go generate ./...

generate-api-serving-cert:
	mkdir -p ./secrets
	go run ./hack/genkey localhost
	mv localhost.* secrets

generate-dev-certs: generate-api-serving-cert

generate-encryption-key:
	go run ./hack/encryption

.PHONY: list
lint:
	gofmt -s -w cmd hack pkg
	go run -mod vendor ./vendor/golang.org/x/tools/cmd/goimports -w -local=github.com/faroshq/faros cmd hack pkg
	go run -mod vendor ./hack/validate-imports cmd hack pkg
	go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

show-sqlite-database:
	sqlitebrowser secrets/database.sqlite3

# Cross-platform CLI resease
build-cli-all:
	GO111MODULE=off go get github.com/mitchellh/gox
	@echo "++ Building faros CLI binaries"
	cd cmd/cli && gox -verbose -output="../../${OUTPUT_BIN_CLI}/{{.OS}}-{{.Arch}}" \
	-ldflags "$(LDFLAGS)" -osarch="linux/amd64 linux/386 darwin/amd64 darwin/arm64 windows/amd64 linux/arm"
	cd cmd/cli && env GOARCH=arm64 GOOS=linux go build -ldflags="$(LDFLAGS)" -o ${OUTPUT_BIN_CLI}/linux-aarch64
	@echo "++ Building synpse CLI OTA metadata"
	GO111MODULE=off go get github.com/sanbornm/go-selfupdate/cmd/go-selfupdate
	cd release && go-selfupdate cli/ $(TAG_NAME)
	./hack/fixup-windows-cli.sh

.PHONY: cli
cli:
	go build -mod vendor -ldflags "$(LDFLAGS)" -o faros ./cmd/cli

.PHONY: faros
faros:
	CGO_ENABLED=1  go build -mod vendor -ldflags "$(LDFLAGS)" -o ${OUTPUT_BIN_FAROS}/faros ./cmd/faros

.PHONY: image-faros
image-faros:
	docker build -t ${FAROS_REPO}:${TAG_NAME} -f dockerfiles/faros/Dockerfile \
	--build-arg version=${TAG_NAME} .

.PHONY: image-cli
image-cli:
	docker build -t ${FAROS_CLI_REPO}:${TAG_NAME} -f dockerfiles/cli/Dockerfile \
	--build-arg version=${TAG_NAME} .

.PHONY: test
test:
	go test -mod=vendor -v -failfast `go list ./... | egrep -v /test/` -coverprofile=profile.cov

.PHONY: test-e2e
test-e2e:
	bash -c "trap './hack/envtest/setup.sh -r' EXIT; source ./hack/envtest/setup.sh -c && \
	go test -count=1 ./test/e2e --tags e2e -test.timeout 5m --test.v"
