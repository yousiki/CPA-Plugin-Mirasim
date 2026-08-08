PLUGIN_ID  := mirasim
# config.go is the single source of truth for the version; the release artifacts and the
# store registry must agree with what plugin.register reports.
VERSION    := $(shell sed -n 's/^const pluginVersion = "\(.*\)"$$/\1/p' config.go)
GOOS       := linux
GOARCH     := amd64
DIST       := dist
ARCHIVE    := $(DIST)/$(PLUGIN_ID)_$(VERSION)_$(GOOS)_$(GOARCH).zip

.PHONY: all build release clean vet check deploy-local

all: build

vet:
	go vet ./...

# Builds dist/mirasim.so for the target container platform.
build:
	@test -n "$(VERSION)" || { echo "could not read pluginVersion from config.go"; exit 1; }
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

check: vet
	@echo "version $(VERSION)"

clean:
	rm -rf $(DIST) panel/dist
