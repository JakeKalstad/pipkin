
.PHONY: all build install extension install-extension test clean

APP := zipdeck
EXT_DIR := extension
VSIX := zipdeck-vscodium-0.3.0.vsix

all: install install-extension

build:
	go build -o $(APP) ./cmd/zipdeck

install: build
	sudo install -m 755 $(APP) /usr/local/bin/$(APP)

extension:
	cd $(EXT_DIR) && npx --yes @vscode/vsce package --no-dependencies --out ../$(VSIX)

install-extension: extension
	codium --install-extension $(VSIX) --force

test:
	go test ./...
	cd $(EXT_DIR) && npm test

clean:
	rm -f $(APP) $(VSIX)
