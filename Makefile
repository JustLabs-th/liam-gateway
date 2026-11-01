.PHONY: test check run worker migrate web generate
CONFIG ?= configs/local.yaml
run:
	go run ./cmd/gateway -config $(CONFIG)
worker:
	go run ./cmd/worker -config $(CONFIG)
migrate:
	go run ./cmd/migrate -config $(CONFIG)
web:
	cd web && pnpm dev
check:
	go vet ./...
	cd web && pnpm typecheck
test:
	go test -race ./...
generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
