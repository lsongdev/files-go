.DEFAULT_GOAL := test

BINARY_NAME=bin/files-go

.PHONY: deps test build clean

deps: web/node_modules/.package-lock.json

web/node_modules/.package-lock.json: web/package.json web/package-lock.json
	npm ci --prefix web

test: deps
	npm test --prefix web
	go test ./...

build: deps
	mkdir -p bin
	GOARCH=arm64 GOOS=darwin go build -ldflags="-s -w" -o ${BINARY_NAME}-darwin-arm64
	GOARCH=amd64 GOOS=darwin go build -ldflags="-s -w" -o ${BINARY_NAME}-darwin-amd64
	GOARCH=amd64 GOOS=linux go build -ldflags="-s -w" -o ${BINARY_NAME}-linux-amd64
	GOARCH=amd64 GOOS=windows go build -ldflags="-s -w" -o ${BINARY_NAME}-windows-amd64.exe

clean:
	go clean
	rm -f ${BINARY_NAME}-darwin-arm64
	rm -f ${BINARY_NAME}-darwin-amd64
	rm -f ${BINARY_NAME}-linux-amd64
	rm -f ${BINARY_NAME}-windows-amd64.exe
