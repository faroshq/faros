# Build context is ./providers/secrets (not the repo root) — see .dockerignore.

FROM node:22-alpine AS portal
WORKDIR /portal
COPY portal/package.json portal/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY portal/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY *.go ./
COPY apis/ ./apis/
COPY backend/ ./backend/
COPY controller/ ./controller/
COPY install/ ./install/
COPY scheme/ ./scheme/
COPY --from=portal /portal/dist ./portal/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/secrets-provider .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/secrets-provider /secrets-provider
# APIResourceSchemas the init subcommand applies (FAROS_SCHEMAS_DIR).
COPY deploy/chart/files/schemas /etc/faros/schemas
EXPOSE 8090
ENV PORT=8090
USER nonroot:nonroot
ENTRYPOINT ["/secrets-provider"]
