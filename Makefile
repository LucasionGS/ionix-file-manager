BIN     := ifm
BIN_PATH := ./build/$(BIN)
PREFIX  ?= /usr/local

.PHONY: build install uninstall clean

build:
	go build -o $(BIN_PATH) .

install: build
	install -Dm755 $(BIN_PATH) $(DESTDIR)$(PREFIX)/bin/$(BIN)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)

clean:
	rm -fr ./build
