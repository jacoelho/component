# disable default rules
.SUFFIXES:
MAKEFLAGS+=-r -R
STATICCHECK_VERSION = v0.8.0-rc.1

default: test

.PHONY: test
test:
	go test -race -shuffle=on -v ./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: ci-tidy
ci-tidy:
	go mod tidy -diff

.PHONY: staticcheck
staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
