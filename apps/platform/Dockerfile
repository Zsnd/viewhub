FROM node:24-bookworm-slim AS web
WORKDIR /build
COPY apps/platform/web/package*.json ./
RUN npm ci
COPY apps/platform/web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps/platform apps/platform
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /viewhub ./apps/platform/cmd/gateway

FROM debian:bookworm-slim AS cloud
COPY --from=build /viewhub /viewhub
COPY --from=web /build/dist /web
ENV WEB_ROOT=/web DB_PATH=/data/viewhub.db
ENTRYPOINT ["/viewhub"]
