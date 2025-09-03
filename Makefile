.DEFAULT_GOAL := build

lint:
	set +e
	# use staticcheck, because golint has been deprecated
	staticcheck ./...
	set -e
.PHONY:lint

vet:
	go vet ./...
	shadow ./...
.PHONY:vet

build: vet lint
	go build -o ./build/k8s-config-reloader .
.PHONY: build

run: vet lint
	mkdir -p ./config-folder
	touch ./config-folder/test.conf
	air
.PHONY: run

deps:
	go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@latest
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install github.com/air-verse/air@latest
	go get -u
	go mod tidy
.PHONY: deps
