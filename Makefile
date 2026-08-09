PLUGIN_ID  := mirasim
VERSION_GO := internal/config/config.go
# internal/config is the single source of truth for the version; the release artifacts and
# the store registry must agree with what plugin.register reports.
VERSION    := $(shell sed -n 's/^const Version = "\(.*\)"$$/\1/p' $(VERSION_GO))
GOOS       := linux
GOARCH     := amd64
DIST       := dist
ARCHIVE    := $(DIST)/$(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip

.PHONY: all build release clean vet test check

all: build

vet:
	go vet ./...

# Everything outside cmd/mirasim is plain Go, so this needs no host and no cgo.
test:
	go test ./...

# Builds dist/mirasim.so for the target container platform.
build:
	@test -n "$(VERSION)" || { echo "could not read Version from $(VERSION_GO)"; exit 1; }
	mkdir -p $(DIST)
	docker build \
		--platform $(GOOS)/$(GOARCH) \
		--file Dockerfile.build \
		--target artifact \
		--output type=local,dest=$(DIST) \
		.
	@file $(DIST)/$(PLUGIN_ID).so || true

# Packages the release assets the CPA plugin store expects. Nothing consumes these today
# — distribution is a file copy — but the layout is kept correct in case that changes:
#   <id>_<version>_<goos>_<goarch>.zip  containing <id>.so at the archive root
#   checksums.txt                        covering every asset in the release
release: build
	cd $(DIST) && rm -f $(notdir $(ARCHIVE)) && zip -q -j $(notdir $(ARCHIVE)) $(PLUGIN_ID).so
	cd $(DIST) && shasum -a 256 $(notdir $(ARCHIVE)) > checksums.txt
	@echo "built $(ARCHIVE)"
	@cat $(DIST)/checksums.txt

check: vet test
	@echo "version $(VERSION)"

clean:
	rm -rf $(DIST) panel/dist
