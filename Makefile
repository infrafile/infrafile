.PHONY: frontend build test fmt vet check browser-test

frontend:
	npm --prefix web ci
	npm --prefix web run build

build: frontend
	go build -o bin/infrafile ./cmd/infrafile

test: frontend
	go test -race ./...

fmt:
	gofmt -w .

vet: frontend
	go vet ./...

# 与 CI 一致：格式 + vet + 测试
check: frontend
	npm --prefix web run typecheck
	npm --prefix web test
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...
	python3 scripts/test_homebrew.py
	node scripts/test_schema.mjs

# Reuse the binary produced by `make build` (also accepts INFRAFILE_BINARY).
browser-test:
	npm --prefix web run test:browser
