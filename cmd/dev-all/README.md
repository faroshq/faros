# Development environment

This directory contains a development environment for faros.

## Certificates

   ```bash
   go run ./hack/genkey proxy
   mv proxy.* dev

   go run ./hack/genkey -client proxy-client
   mv proxy-client.* dev

   go run ./hack/genkey -client localhost
   mv localhost.* dev
   ```
