'use strict';
const vscode = require('vscode');
const os = require('node:os');
const path = require('node:path');
const fs = require('node:fs/promises');

async function listSessions(root, outputDirectory) {
  const base = path.join(outputDirectory || path.join(os.homedir(), 'pipkin', 'outputs'), 'jobs', path.basename(root));
  let entries;
  try { entries = await fs.readdir(base, {withFileTypes:true}); }
  catch (err) { if (err.code === 'ENOENT') return []; throw err; }
  const jobs = [];
  for (const entry of entries) {
    if (!entry.isDirectory()) continue;
    const dir = path.join(base, entry.name);
    try {
      const job = JSON.parse(await fs.readFile(path.join(dir, 'job.json'), 'utf8'));
      if (job.root && path.resolve(job.root) === path.resolve(root)) jobs.push({dir, job});
    } catch { /* a delivery may still be preparing its metadata */ }
  }
  return jobs.sort((a, b) => String(b.job.created).localeCompare(String(a.job.created))).slice(0, 100);
}

class SessionsProvider {
  constructor(getRoot, getOutput) {
    this.getRoot = getRoot;
    this.getOutput = getOutput;
    this.emitter = new vscode.EventEmitter();
    this.onDidChangeTreeData = this.emitter.event;
  }
  refresh() { this.emitter.fire(); }
  getTreeItem(item) { return item; }
  async getChildren() {
    let root;
    try { root = this.getRoot(); } catch { return []; }
    return (await listSessions(root, this.getOutput())).map(({dir, job}) => {
      const item = new vscode.TreeItem(job.id, vscode.TreeItemCollapsibleState.None);
      item.description = `${job.status} · ${job.model || job.provider}`;
      item.tooltip = `${job.created}\n${dir}`;
      item.iconPath = new vscode.ThemeIcon(job.status === 'completed' ? 'git-compare' : job.status === 'failed' ? 'error' : 'loading');
      item.command = {command:'pipkin.reviewJob', title:'Review Pipkin Delivery', arguments:[dir]};
      return item;
    });
  }
}
module.exports = {SessionsProvider, listSessions};
