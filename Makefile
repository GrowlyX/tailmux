# Build and install tailmux from source, on any Linux (or macOS/BSD for
# the CLI). Needs Go; the desktop app also needs Rust, Node and pnpm and
# the WebKitGTK libraries (docs/install.md#build-from-source).
#
#   make && sudo make install                  the CLI, /usr/local/bin/tailmux
#   make desktop && sudo make install-desktop  plus the tray app
#   sudo make uninstall
#
# Source builds never replace themselves with a release binary; they
# tell you when there's a new release (selfUpdate=off).

PREFIX  ?= /usr/local
DESTDIR ?=
BINDIR  := $(DESTDIR)$(PREFIX)/bin
DATADIR := $(DESTDIR)$(PREFIX)/share
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)
GO      ?= go
# Distro Go packages often pin GOTOOLCHAIN=local; if theirs is older than
# go.mod asks for, let Go fetch the right toolchain. GOTOOLCHAIN=local
# make to refuse instead.
export GOTOOLCHAIN ?= auto
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.selfUpdate=off

TRIPLE   = $(shell rustc -vV | sed -n 's/^host: //p')
TAURI    := desktop/src-tauri
DESKTOP  := $(TAURI)/target/release/tailmux-desktop

SOURCES := go.mod go.sum $(shell find cmd internal -name '*.go' 2>/dev/null)

.PHONY: all desktop install install-desktop uninstall clean

all: tailmux

# A file target, so `sudo make install` uses what you built instead of
# rebuilding as root.
tailmux: $(SOURCES)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o tailmux ./cmd/tailmux

# The desktop app finds tailmux next to itself, so the CLI is installed
# alongside it. --no-bundle: just the binary, no .deb/.rpm/AppImage.
desktop: tailmux
	cp tailmux $(TAURI)/binaries/tailmux-$(TRIPLE)
	cd desktop && pnpm install --frozen-lockfile && pnpm tauri build --no-bundle

install: tailmux
	install -d $(BINDIR)
	install -m755 tailmux $(BINDIR)/tailmux

install-desktop: install
	install -d $(DATADIR)/applications $(DATADIR)/icons/hicolor/128x128/apps $(DATADIR)/icons/hicolor/32x32/apps
	install -m755 $(DESKTOP) $(BINDIR)/tailmux-desktop
	install -m644 packaging/linux/tailmux.desktop $(DATADIR)/applications/tailmux.desktop
	install -m644 $(TAURI)/icons/128x128.png $(DATADIR)/icons/hicolor/128x128/apps/tailmux.png
	install -m644 $(TAURI)/icons/32x32.png $(DATADIR)/icons/hicolor/32x32/apps/tailmux.png

uninstall:
	rm -f $(BINDIR)/tailmux $(BINDIR)/tailmux-desktop $(DATADIR)/applications/tailmux.desktop \
		$(DATADIR)/icons/hicolor/128x128/apps/tailmux.png $(DATADIR)/icons/hicolor/32x32/apps/tailmux.png

clean:
	rm -f tailmux
