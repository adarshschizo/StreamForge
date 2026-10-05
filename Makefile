.PHONY: test build run-api run-worker infra-up infra-down

test:
	go test ./...

build:
	go build ./apps/api ./apps/worker

run-api:
	go run ./apps/api

run-worker:
	go run ./apps/worker

infra-up:
	docker compose up -d postgres redis minio

infra-down:
	docker compose down
