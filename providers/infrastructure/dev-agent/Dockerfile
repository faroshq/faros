# syntax=docker/dockerfile:1
# kedge-dev-agent — the injected control process for development-mode
# components of any infrastructure Template (docs/app-studio-template-sandboxes.md §2).
# The image is scratch on purpose: it exists only to carry the static binary
# that an init container copies into the component's toolchain image
# (`kedge-dev-agent --install /kedge/bin`) and the canonical server-only
# Actions SDK payload. The SDK is copied from provider-sdk/actions-node at image
# build time; it is installed into the shared /kedge/bin volume at runtime, so
# nothing kedge-specific is baked into dev images. The build context is the
# repository root (see Makefile's docker-build-dev-agent target).
FROM golang:1.26 AS build
WORKDIR /src
COPY providers/infrastructure/dev-agent/ ./
COPY provider-sdk/actions-node/ /kedge-actions-node/
# The checkout intentionally keeps SDK source files private (0600/0700). The
# scratch runtime is executed by the non-root UID 1000 init container, so
# normalize only the image copy; never mutate canonical source permissions.
RUN chmod 0755 /kedge-actions-node \
    && chmod 0644 /kedge-actions-node/package.json /kedge-actions-node/index.mjs /kedge-actions-node/index.d.ts
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kedge-dev-agent .

FROM scratch
COPY --from=build /out/kedge-dev-agent /kedge-dev-agent
COPY --from=build /kedge-actions-node /kedge-actions-node
ENTRYPOINT ["/kedge-dev-agent"]
