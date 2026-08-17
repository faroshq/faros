# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/deployments-provider .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/deployments-provider /deployments-provider
COPY deploy/chart/files/schemas /etc/faros/schemas
EXPOSE 8093
ENV PORT=8093
USER nonroot:nonroot
ENTRYPOINT ["/deployments-provider"]
