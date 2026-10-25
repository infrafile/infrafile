.PHONY: build test fmt vet check

build:
	go build -o bin/infrafile ./cmd/infrafile

test:
	go test -race ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

# 与 CI 一致：格式 + vet + 测试
check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race ./...
	python3 scripts/test_homebrew.py
