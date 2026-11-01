# syntax=docker/dockerfile:1
FROM golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build
WORKDIR /src
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/lab-client ./cmd/lab-client
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/lab-client ./cmd/lab-client

FROM scratch
COPY --from=build /out/lab-client /lab-client
USER 65532:65532
ENTRYPOINT ["/lab-client"]
