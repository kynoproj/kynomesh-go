.PHONY: all
all: test

.PHONY: test
test:
	go test $(shell go list ./... | grep -v /vendor/) -race -short -v -timeout 60s

.PHONY: test-coverage
test-coverage:
	go test -v -timeout 7m -covermode=atomic -coverprofile=profile.cov $(shell go list ./... | grep -v /vendor/)
	go tool cover -func=profile.cov

$(GOPATH)/bin/golangci-lint:
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(GOPATH)/bin v2.12.2

.PHONY: lint
lint: $(GOPATH)/bin/golangci-lint
	go mod tidy
	$(GOPATH)/bin/golangci-lint run --fix --verbose --concurrency 4 --timeout 5m
