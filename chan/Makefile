PREFIX ?= /usr/local/bin

.PHONY: all release-local release install clean build-tui-launcher

all: release-local

release-local:
	$(MAKE) -C tui release-local

release:
	$(MAKE) -C tui release

install:
	$(MAKE) -C tui install PREFIX=$(PREFIX)

clean:
	$(MAKE) -C tui clean

build-tui-launcher:
	$(MAKE) -C tui build-tui-launcher