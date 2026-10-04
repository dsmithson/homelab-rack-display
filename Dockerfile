# syntax=docker/dockerfile:1
# Multi-arch image: Go binary + Debian's Chromium for the panel renderer.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/rack-display ./cmd/rack-display

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium ca-certificates tzdata tini \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/rack-display /usr/local/bin/rack-display
COPY config/display.json /etc/rack-display/display.json
ENV CHROME_PATH=/usr/bin/chromium \
    TZ=America/Phoenix
EXPOSE 8080
# tini as PID 1 reaps the Chromium helpers orphaned on each browser recycle.
ENTRYPOINT ["tini", "--", "rack-display", "-config", "/etc/rack-display/display.json"]
