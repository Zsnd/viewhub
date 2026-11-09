FROM node:24-bookworm-slim AS web
WORKDIR /build
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /viewhub ./cmd/gateway && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /viewhub-edge ./cmd/edge

FROM debian:bookworm-slim AS edge
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /viewhub-edge /viewhub-edge
ENTRYPOINT ["/viewhub-edge"]

FROM build AS edge-release
RUN mkdir /release && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /release/viewhub-edge-amd64 ./cmd/edge && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /release/viewhub-edge-arm64 ./cmd/edge

FROM debian:bookworm-slim AS cloud
COPY --from=edge-release /release /release
COPY --from=build /viewhub /viewhub
COPY --from=web /build/dist /web
ENV WEB_ROOT=/web DB_PATH=/data/viewhub.db
ENTRYPOINT ["/viewhub"]
