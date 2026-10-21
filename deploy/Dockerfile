# SPDX-FileCopyrightText: 2026 CubieCode
# SPDX-License-Identifier: MIT
#
# Runtime image for wardlockd. GoReleaser builds the static binary and puts it
# under <os>/<arch>/ in the build context (.goreleaser.yaml, dockers_v2); the
# last stage only packages it. Data lives in the /data volume.
#
# The `source` target builds wardlockd and the embedded dashboard from a
# checkout instead, for running before an image is published
# (docs/DEPLOYMENT.md, "VPS with automatic TLS and R2"). From the repository
# root:
#   docker build -f deploy/Dockerfile --target source -t wardlock:source .
# BuildKit builds only the stages a target needs, so release builds never run
# the source stages and source builds never need GoReleaser's binaries.

# Distroless has no shell, so create the data directory, owned by the
# nonroot user (65532), in a throwaway stage. A new volume inherits it. The
# stage runs on the build platform, so arm64 images need no emulation.
FROM --platform=$BUILDPLATFORM busybox:1.37 AS data
RUN mkdir /data && chown 65532:65532 /data && chmod 0700 /data

# Source build, step 1: the dashboard (apps/web/dist), as `pnpm build` does.
FROM --platform=$BUILDPLATFORM node:24.22.0-bookworm-slim AS web
WORKDIR /src
ENV TURBO_TELEMETRY_DISABLED=1
RUN corepack enable
COPY . .
RUN pnpm install --frozen-lockfile \
    && pnpm exec turbo build --filter @wardlock/web

# Source build, step 2: wardlockd with the dashboard embedded, as
# scripts/build-go.sh does. Cross-compiles for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS server
ARG TARGETOS TARGETARCH
ARG WARDLOCK_VERSION=dev
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /src
COPY go.work ./
COPY apps/server apps/server
COPY apps/cli apps/cli
COPY packages/apiclient-go packages/apiclient-go
COPY --from=web /src/apps/web/dist apps/server/internal/dashboard/ui/dist
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X wardlock.nz/server/internal/platform.Version=$WARDLOCK_VERSION" \
      -o /out/wardlockd ./apps/server/cmd/wardlockd

# Source build, step 3: the same runtime image as the release, from source.
FROM gcr.io/distroless/static-debian12:nonroot AS source
COPY --from=data --chown=65532:65532 /data /data
COPY --from=server /out/wardlockd /usr/local/bin/wardlockd
ENV WARDLOCK_DATA_DIR=/data \
    WARDLOCK_HTTP_ADDR=:8080 \
    WARDLOCK_LOG_FORMAT=json
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/wardlockd"]
CMD ["serve"]

# Release image (GoReleaser). Keep it the last stage: it is the default target.
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY --from=data --chown=65532:65532 /data /data
COPY $TARGETPLATFORM/wardlockd /usr/local/bin/wardlockd
ENV WARDLOCK_DATA_DIR=/data \
    WARDLOCK_HTTP_ADDR=:8080 \
    WARDLOCK_LOG_FORMAT=json
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/wardlockd"]
CMD ["serve"]
