.PHONY: all clean release release-local install build-portable-launcher-bundle build-tui-launcher

all: release-local

release-local release install clean:
	$(MAKE) -C .. $@

build-portable-launcher-bundle build-tui-launcher:
	$(MAKE) -C .. build-tui-launcher
