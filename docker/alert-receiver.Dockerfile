# syntax=docker/dockerfile:1
FROM golang:1.26.10-alpine3.23@sha256:34414731924cf2263160f0021f5cf6e1807382d85248f7cb419d4187b96d5e43 AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/alert-receiver ./cmd/alert-receiver
ARG REVISION=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.revision=${REVISION}" -o /out/alert-receiver ./cmd/alert-receiver
FROM scratch
COPY --from=build /out/alert-receiver /alert-receiver
USER 65532:65532
EXPOSE 8085
ENTRYPOINT ["/alert-receiver"]
