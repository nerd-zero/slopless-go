SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help build test vet fmt lint selfcheck check install install-hooks version release

help:  ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'

build:  ## Build every binary
	go build ./...

test:  ## Run tests
	go test ./... -race -count=1

vet:  ## go vet
	go vet ./...

fmt:  ## Check formatting (gofmt)
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi

lint: vet fmt  ## vet + format-check

selfcheck:  ## Run every slopless-go check against this repo (advisory)
	go run ./cmd/slopless-go all ./...

check: lint test  ## Everything CI runs

install: install-hooks  ## Local dev setup
	go mod download

install-hooks:  ## Point git at the tracked .githooks directory
	git config core.hooksPath .githooks

version:  ## Preview the next CalVer version
	./scripts/version.sh --dry-run

release:  ## Bump VERSION, commit, tag, and push
	./scripts/version.sh
