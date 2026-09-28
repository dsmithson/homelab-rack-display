NODE ?= 192.168.86.144
BIN  := bin/displaytest-arm64

.PHONY: build preview deploy run
build:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/displaytest

preview:
	go run ./cmd/displaytest -out preview.png

deploy: build
	scp -q $(BIN) $(NODE):~/displaytest

run: deploy
	ssh -t $(NODE) '~/displaytest'
