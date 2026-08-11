# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25.12-alpine AS go-builder
ARG ROTAKEY_VERSION=0.2.4
ARG ROTAKEY_COMMIT=unknown
ARG ROTAKEY_BUILD_TIME=unknown
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/web/dist /web-dist
RUN rm -rf internal/app/webdist \
    && mkdir -p internal/app/webdist \
    && cp -R /web-dist/. internal/app/webdist/ \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X github.com/jisunahamed/rotakey/internal/app.Version=${ROTAKEY_VERSION} -X github.com/jisunahamed/rotakey/internal/app.BuildCommit=${ROTAKEY_COMMIT} -X github.com/jisunahamed/rotakey/internal/app.BuildTime=${ROTAKEY_BUILD_TIME}" -o /out/rotakey ./cmd/gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S rotakey \
    && adduser -S -G rotakey -h /app rotakey
WORKDIR /app
COPY --from=go-builder /out/rotakey /usr/local/bin/rotakey
USER rotakey
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rotakey"]
