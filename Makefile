SHELL = /bin/bash
OUTPUT_BIN_CLI ?= release/cli
TAG_NAME ?= $(shell git describe --tags --abbrev=0)

run:
	FAROS_DATABASE_SQLITE_URI=secrets/database.sqlite3
	FAROS_API_TLS_KEY=secrets/localhost.key \
	FAROS_API_TLS_CERT=secrets/localhost.crt \
	go run  ./cmd/faros --loglevel=trace

generate:
	go generate ./...

generate-api-serving-cert:
	mkdir -p ./secrets
	go run ./hack/genkey localhost
	mv localhost.* secrets

generate-dev-certs: generate-api-serving-cert

generate-encryption-key:
	go run ./hack/encryption

lint:
	gofmt -s -w cmd hack pkg
	go run -mod vendor ./vendor/golang.org/x/tools/cmd/goimports -w -local=github.com/faroshq/faros cmd hack pkg
	go run -mod vendor ./hack/validate-imports cmd hack pkg
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

cli:
	CGO_ENABLED=0 go build -mod vendor -ldflags "$(LDFLAGS)" -o faros ./cmd/cli

