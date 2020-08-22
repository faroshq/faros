SHELL = /bin/bash
COMMIT = $(shell git rev-parse --short HEAD)$(shell [[ $$(git status --porcelain) = "" ]] || echo -dirty)
OPERATOR_IMAGE ?="quay.io/faroshq/faros-operator:latest"

operator: generate
	go build -ldflags "-X github.com/faroshq/faros/pkg/util/version.GitCommit=$(COMMIT)" ./cmd/faros

generate:
	go generate ./...

image-operator: operator
	docker pull registry.access.redhat.com/ubi8/ubi-minimal
	docker build -f Dockerfile.operator -t $(OPERATOR_IMAGE) .

publish-operator: image-operator
	docker push $(OPERATOR_IMAGE)

test-go: generate
	go build ./...

	gofmt -s -w cmd hack pkg test
	go run ./vendor/golang.org/x/tools/cmd/goimports -w -local=github.com/faroshq/faros cmd hack pkg test
	go run ./hack/validate-imports cmd hack pkg test
	@[ -z "$$(ls pkg/util/*.go 2>/dev/null)" ] || (echo error: go files are not allowed in pkg/util, use a subpackage; exit 1)
	@[ -z "$$(find -name "*:*")" ] || (echo error: filenames with colons are not allowed on Windows, please rename; exit 1)
	go test -tags e2e -run ^$$ ./test/e2e/...

	go vet ./...
	set -o pipefail && go test -v ./... -coverprofile cover.out | tee uts.txt

lint-go: generate
	golangci-lint run

.PHONY: operator clean generate image-operator publish-operator test-go
