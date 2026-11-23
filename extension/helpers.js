'use strict';
const path = require('node:path');

function relativePaths(root, paths) {
  const result = new Set();
  for (const p of paths) {
    const rel = path.relative(root, p);
    if (rel === '' || path.isAbsolute(rel) || rel === '..' || rel.startsWith('..' + path.sep)) {
      throw new Error(`Path is outside the workspace or is the workspace itself: ${p}`);
    }
    result.add(rel.split(path.sep).join('/'));
  }
  return [...result].sort();
}
function filePath(uri) {
  if (!uri || uri.scheme !== 'file') throw new Error('Only local files are supported.');
  return uri.fsPath;
}
function stripTrailingSlash(url) { return url.replace(/\/+$/, ''); }
module.exports = { relativePaths, filePath, stripTrailingSlash };
