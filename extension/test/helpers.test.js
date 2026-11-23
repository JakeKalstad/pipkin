'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { relativePaths, filePath, stripTrailingSlash } = require('../helpers');
test('relative paths deduplicated and sorted', () => {
 assert.deepEqual(relativePaths('/repo', ['/repo/b.go', '/repo/a.go', '/repo/a.go']), ['a.go','b.go']);
});
test('prevent paths outside workspace', () => {
 assert.throws(() => relativePaths('/repo', ['/other/file']), /outside/);
 assert.throws(() => relativePaths('/repo', ['/repo']), /outside/);
});
test('local file URI only', () => {
 assert.equal(filePath({scheme:'file',fsPath:'/test'}), '/test');
 assert.throws(() => filePath({scheme:'untitled',fsPath:'/test'}), /Only local/);
});
test('baseURL trimming', () => assert.equal(stripTrailingSlash('http://localhost:8000/v1///'), 'http://localhost:8000/v1'));
