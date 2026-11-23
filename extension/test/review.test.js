'use strict';
// vscode module is supplied by VS Code at runtime; stub only for pure helper test.
const test = require('node:test'); const assert = require('node:assert/strict'); const Module = require('node:module');
const load = Module._load;
Module._load = function (name, ...rest) { if (name === 'vscode') return {}; return load.call(this, name, ...rest); };
const { escapeHtml } = require('../review');
Module._load = load;
test('HTML escaping prevents source-controlled paths injecting review markup', () => assert.equal(escapeHtml('<img src="x">'), '&lt;img src=&quot;x&quot;&gt;'));
