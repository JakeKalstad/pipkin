
.PHONY: all build install extension install-extension test test-e2e clean

APP := pipkin
EXT_DIR := extension
VSIX := pipkin-vscodium-0.15.0.vsix

all: install install-extension

build:
	go build -o $(APP) ./cmd/pipkin

install: build
	sudo install -m 755 $(APP) /usr/local/bin/$(APP)

extension:
	cd $(EXT_DIR) && npx --yes @vscode/vsce package --no-dependencies --out ../$(VSIX)

install-extension: extension
	codium --install-extension $(VSIX) --force

test:
	go test ./...
	cd $(EXT_DIR) && npm test

test-e2e: build
	python3 tests/e2e.py ./$(APP)

clean:
	rm -f $(APP) $(VSIX)
