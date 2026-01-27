# syntax=docker/dockerfile:1.4

ARG GO_VERSION="1.25.6"

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build-binaries

WORKDIR /work
COPY go.mod go.sum ./

RUN \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . ./

RUN \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -f && \
    GOOS=linux GOARCH=amd64 \
    CGO_ENABLED=0 \
    go build \
        -tags kqueue \
        -ldflags "${GO_LDFLAGS} -X storj.io/common/version.buildRelease=true" \
        -o /out/linux_amd64/ storj.io/storjscan/cmd/storjscan

RUN \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    set -f && \
    GOOS=linux GOARCH=arm64 \
    CGO_ENABLED=0 \
    go build \
        -tags kqueue \
        -ldflags "${GO_LDFLAGS} -X storj.io/common/version.buildRelease=true" \
        -o /out/linux_arm64/ storj.io/storjscan/cmd/storjscan


FROM scratch AS export-binaries
COPY --from=build-binaries /out/linux_amd64 /linux_amd64
COPY --from=build-binaries /out/linux_arm64 /linux_arm64
