# Makefile for the Docker image quay.io/glerchundi/kubelistener
# MAINTAINER: Gorka Lerchundi Osa <glertxundi@gmail.com>
# If you update this image please bump the tag value before pushing.

.PHONY: all build test static container push clean

VERSION = 0.1.0
PREFIX = quay.io/glerchundi

all: build

build:
	@echo "Building etcd2-bootstrapper..."
	ROOTPATH=$(shell pwd -P); \
	GO15VENDOREXPERIMENT=1 go build -o $$ROOTPATH/bin/etcd2-bootstrapper

test:
	@echo "Running tests..."
	GO15VENDOREXPERIMENT=1 go test

static:
	ROOTPATH=$(shell pwd -P); \
	mkdir -p $$ROOTPATH/bin; \
	GO15VENDOREXPERIMENT=1 \
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build \
		-a -tags netgo -installsuffix cgo -ldflags '-extld ld -extldflags -static' -a -x \
		-o $$ROOTPATH/bin/etcd2-bootstrapper-linux-amd64 \
		.

container: static
	docker build -t $(PREFIX)/etcd2-bootstrapper:$(VERSION) .

push: container
	docker push $(PREFIX)/etcd2-bootstrapper:$(VERSION)

clean:
	rm -f bin/etcd2-bootstrapper*
