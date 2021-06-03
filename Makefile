BINARY_NAME=collector

all: build

build:
	go build -o $(BINARY_NAME) main.go