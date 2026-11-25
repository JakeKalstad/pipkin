
.PHONY: all build install extension install-extension install-xlsx test test-e2e clean

APP := pipkin
EXT_DIR := extension
VSIX := pipkin-vscodium-0.16.0.vsix

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

install-xlsx:
	@set -eu; \
		config="$${PIPKIN_CONFIG:-$$HOME/.config/pipkin/config.json}"; \
		venv="$$(dirname "$$config")/xlsx-venv"; \
		python3 -m venv "$$venv"; \
		"$$venv/bin/python" -m pip install 'openpyxl>=3.1,<4'
