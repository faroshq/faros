# syntax=docker/dockerfile:1

# 1. Build the portal bundle before compiling the provider so the
#    image contains the exact assets served by the binary.
FROM node:22-alpine AS portal
WORKDIR /portal
COPY providers/deployments/portal/package.json providers/deployments/portal/package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY providers/deployments/portal/ ./
RUN npm run build

# 2. Build the Go provider with the freshly generated portal/dist overlay.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY providers/deployments/go.mod providers/deployments/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY providers/deployments/ ./
COPY --from=portal /portal/dist ./portal/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/deployments-provider .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/deployments-provider /deployments-provider
COPY providers/deployments/deploy/chart/files/schemas /etc/faros/schemas
EXPOSE 8093
ENV PORT=8093
USER nonroot:nonroot
ENTRYPOINT ["/deployments-provider"]
