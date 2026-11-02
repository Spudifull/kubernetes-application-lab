# syntax=docker/dockerfile:1
FROM golang:1.26.10-alpine3.23@sha256:34414731924cf2263160f0021f5cf6e1807382d85248f7cb419d4187b96d5e43 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG REVISION=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION} -X main.buildDate=${BUILD_DATE}" \
    -o /out/lab-publisher ./cmd/publisher
FROM scratch
COPY --from=build /out/lab-publisher /lab-publisher
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/lab-publisher"]
