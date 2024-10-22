arch        := amd64
os          := $(shell uname -s | tr '[:upper:]' '[:lower:]')

test:
	go test ./... -v -count=1 -coverprofile cover.out

.PHONY:
all: setup promote finalize rollback

setup:
	go build -installsuffix cgo -o bin/setup cmd/setup/main.go

promote:
	go build -installsuffix cgo -o bin/promote cmd/promote/main.go

finalize:
	go build -installsuffix cgo -o bin/finalize cmd/finalize/main.go

rollback:
	go build -installsuffix cgo -o bin/rollback cmd/rollback/main.go

check:
	go run honnef.co/go/tools/cmd/staticcheck ./...
	go run golang.org/x/vuln/cmd/govulncheck ./...
