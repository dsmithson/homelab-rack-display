NODE  ?= 192.168.86.144
IMAGE ?= dsmithson/rack-display
BIN   := bin/displaytest-arm64

.PHONY: build test preview deploy run app image render
build:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/displaytest

test:
	go vet ./... && go test ./...

# Local rack-display build (host arch).
app:
	go build -o bin/rack-display ./cmd/rack-display

# Multi-arch image; add --push once the registry is decided.
image:
	docker buildx build --builder default --platform linux/arm64,linux/amd64 -t $(IMAGE):dev .

# Re-render design/renders from the screens' built-in mock data.
render:
	design/render.sh

preview:
	go run ./cmd/displaytest -out preview.png

deploy: build
	scp -q $(BIN) $(NODE):~/displaytest

run: deploy
	ssh -t $(NODE) '~/displaytest'
