# syntax=docker/dockerfile:1

FROM node:20-alpine AS frontend
WORKDIR /src/server/frontend
COPY server/frontend/package.json server/frontend/package-lock.json ./
RUN npm ci
COPY server/frontend/ ./
RUN npm run build

FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/server/frontend/dist ./server/frontend/dist
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /out/tinyice .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 tinyice && \
    mkdir -p /data && chown tinyice:tinyice /data
COPY --from=builder /out/tinyice /usr/local/bin/tinyice
USER tinyice
WORKDIR /data
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/tinyice"]
CMD ["-port", "8080", "-config", "/data/config.json"]
