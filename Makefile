.PHONY: test lint vet clean

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	go clean -testcache

.DEFAULT_GOAL := test
