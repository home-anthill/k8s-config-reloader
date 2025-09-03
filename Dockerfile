# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder
RUN apk update && apk add --no-cache \
    make gcc musl-dev

WORKDIR /app
# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading them in subsequent builds if they change
COPY go.mod ./
COPY go.sum ./
RUN go mod download && go mod verify

COPY . ./

RUN make deps

RUN make build

FROM golang:1.25-alpine
WORKDIR /
COPY --from=builder /app/build/k8s-config-reloader /k8s-config-reloader

ENTRYPOINT ["/k8s-config-reloader"]
