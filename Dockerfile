# syntax=docker/dockerfile:1

FROM node:24-alpine AS web-build
WORKDIR /src
COPY web/package*.json ./web/
RUN cd web && npm ci
COPY web ./web
RUN mkdir -p internal/web && cd web && npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/internal/web/dist ./internal/web/dist
# CGO disabled: modernc.org/sqlite is pure Go, so we get a static binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# ponytail: alpine not scratch - need ca-certificates for HTTPS to openrouter.ai
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    adduser -D -u 10001 app && \
    mkdir -p /data && chown app:app /data
USER app
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
ENV DB_PATH=/data/gateway.db \
    LISTEN_ADDR=:8080 \
    AIHUBMIX_LISTEN_ADDR=:8081 \
    GOOGLE_LISTEN_ADDR=:8082
VOLUME /data
EXPOSE 8080 8081 8082
ENTRYPOINT ["/app/gateway"]
