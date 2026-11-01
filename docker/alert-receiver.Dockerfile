# syntax=docker/dockerfile:1
FROM golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build
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
