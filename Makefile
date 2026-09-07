GO = go
OUTPUT ?= slaxi
LDFLAGS = -s -w -buildid=
GCFLAGS = all=-l

include ../check.mk

.PHONY: build clean
build:
	CGO_ENABLED=0 $(GO) build -mod=readonly -trimpath -buildvcs=false -gcflags='$(GCFLAGS)' -ldflags='$(LDFLAGS)' -o $(OUTPUT) .

clean:
	rm -f $(OUTPUT)
