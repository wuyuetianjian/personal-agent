.PHONY: build test smoke clean

build:
	go build -o bin/pachat ./cmd/pachat

test:
	go test ./...

smoke:
	go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"

clean:
	rm -rf bin
