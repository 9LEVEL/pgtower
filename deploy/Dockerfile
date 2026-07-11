# syntax=docker/dockerfile:1

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /pgtui .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pgtui /pgtui
# TUI: rode com TTY -> docker run -it --rm --env-file .env ghcr.io/9level/pg-tui
ENTRYPOINT ["/pgtui"]
