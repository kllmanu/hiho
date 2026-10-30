.PHONY: build install

BINARIES := hi ho
BINDIR ?= $(HOME)/.local/bin

build: $(BINARIES)

hi: hi.go
	go build -o $@ hi.go

ho: ho.go
	go build -o $@ ho.go

install: build
	mkdir -p "$(BINDIR)"
	install -m 755 $(BINARIES) "$(BINDIR)"
