arch        := amd64
os          := $(shell uname -s | tr '[:upper:]' '[:lower:]')

test:
	go test ./... -v -count=1 -coverprofile cover.out

.PHONY:
all: setup promote cleanup rollback

setup:
	go build -installsuffix cgo -o bin/setup cmd/setup/main.go

promote:
	go build -installsuffix cgo -o bin/promote cmd/promote/main.go

cleanup:
	go build -installsuffix cgo -o bin/cleanup cmd/cleanup/main.go

rollback:
	go build -installsuffix cgo -o bin/rollback cmd/rollback/main.go
