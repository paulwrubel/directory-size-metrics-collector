BINARY_NAME=collector

all: build

build:
	go build -o $(BINARY_NAME) main.go

build-linux:
	GOOS=linux \
	GOARCH=amd64 \
	CGO_ENABLED=0 \
	go build -o $(BINARY_NAME) main.go
