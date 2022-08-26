SHELL = /bin/bash

run:
	FAROS_DATABASE_SQLITE_URI=secrets/database.sqlite3
	FAROS_API_TLS_KEY=secrets/localhost.key \
	FAROS_API_TLS_CERT=secrets/localhost.crt \
	go run  ./cmd/faros --loglevel=debug

build-cli:
	go build -o faros ./cmd/cli

generate-api-serving-cert:
	mkdir -p ./secrets
	go run ./hack/genkey localhost
	mv localhost.* secrets


generate-dev-certs: generate-api-serving-cert
