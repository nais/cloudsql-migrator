arch        := amd64
os          := $(shell uname -s | tr '[:upper:]' '[:lower:]')

.PHONY:
all: test setup promote cleanup

test:
	go test ./... -v -count=1 -coverprofile cover.out

setup:
	go build -a -installsuffix cgo -o bin/setup cmd/setup/main.go

promote:
	go build -a -installsuffix cgo -o bin/promote cmd/promote/main.go

cleanup:
	go build -a -installsuffix cgo -o bin/cleanup cmd/cleanup/main.go
