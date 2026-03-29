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

test: vet lint
	mkdir -p ./coverage
	ENV=testing go test -v -race -count=1 -coverpkg ./... -coverprofile ./coverage/profile.cov ./...
	# go tool cover -html ./coverage/profile.cov
	go tool cover -html ./coverage/profile.cov -o ./coverage/cover.html
	go-cover-treemap -coverprofile ./coverage/profile.cov > ./coverage/out.svg
.PHONY: test

deps:
	go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@latest
	go install honnef.co/go/tools/cmd/staticcheck@latest
	go install github.com/air-verse/air@latest
	go get -u
	go mod tidy
	go install github.com/nikolaydubina/go-cover-treemap@latest
.PHONY: deps
