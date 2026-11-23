'use strict';
const vscode = require('vscode');
const path = require('node:path');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');

function runJSON(executable, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { shell: false });
    let out = '', err = '';
    child.stdout.on('data', data => out += data);
    child.stderr.on('data', data => err += data);
    child.on('error', reject);
    child.on('close', code => {
      if (code !== 0) return reject(new Error(err || `Zipdeck exit ${code}`));
      try { resolve(JSON.parse(out)); } catch (e) { reject(e); }
    });
  });
}
function escapeHtml(text) { return String(text).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
async function showReview(executable, jobDir, output) {
  const review = await runJSON(executable, ['--review', jobDir]);
  const job = JSON.parse(await fs.readFile(path.join(jobDir, 'job.json'), 'utf8'));
  const panel = vscode.window.createWebviewPanel('zipdeckReview', 'Zipdeck: Review changes', vscode.ViewColumn.Beside, { enableScripts: true, retainContextWhenHidden: true });
  const files = review.files || [];
  const badge = s => ({ added: 'New', deleted: 'Removed', modified: 'Changed' }[s] || s);
  const rows = files.map((f, i) => `<div class="file"><div><b>${escapeHtml(f.path)}</b><span class="tag">${badge(f.status)}</span></div><div class="actions"><button data-action="diff" data-index="${i}">View differences</button><button data-action="apply" data-index="${i}" class="apply">Apply this file</button></div></div>`).join('');
  panel.webview.html = `<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-zipdeck-local';"><style>body{font-family:var(--vscode-font-family);color:var(--vscode-foreground);padding:22px}h1{font-size:20px}h2{font-size:15px}.sub{color:var(--vscode-descriptionForeground)}.stats{display:flex;gap:12px;margin:16px 0}.stat{background:var(--vscode-editorWidget-background);padding:12px;border-radius:7px;min-width:90px}.stat strong{display:block;font-size:20px}.file{border-bottom:1px solid var(--vscode-panel-border);padding:13px 0;display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap}.tag{font-size:11px;margin-left:10px;color:var(--vscode-descriptionForeground)}button{cursor:pointer;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground);border:0;padding:7px 11px;border-radius:4px}button.apply{background:var(--vscode-button-background);color:var(--vscode-button-foreground)}.actions{display:flex;gap:8px}code{overflow-wrap:anywhere}</style></head><body><h1>Review model changes</h1><p class="sub">This is a proposal, not an applied patch. Open the native side-by-side diff for each file, then apply only what you approve.</p><div class="stats"><div class="stat"><strong>${files.length}</strong>Changed files</div><div class="stat"><strong>${job.input_tokens ?? '—'}</strong>Input tokens</div><div class="stat"><strong>${job.output_tokens ?? '—'}</strong>Output tokens</div><div class="stat"><strong>${job.cached_tokens ?? '—'}</strong>Cached tokens</div></div><p class="sub">Model: ${escapeHtml(job.model || 'Unknown')} · Session: <code>${escapeHtml(job.id)}</code></p><h2>Files</h2>${rows || '<p>No file changes detected.</p>'}<p class="sub">The original and modified ZIPs are saved in <code>${escapeHtml(jobDir)}</code>.</p><script nonce="zipdeck-local">const vscode=acquireVsCodeApi();document.addEventListener('click',ev=>{const button=ev.target.closest('button[data-action]');if(button) vscode.postMessage({action:button.dataset.action,index:Number(button.dataset.index)});});</script></body></html>`;
  panel.webview.onDidReceiveMessage(async msg => {
    const f = files[msg.index]; if (!f || !['diff', 'apply'].includes(msg.action)) return;
    try {
      if (msg.action === 'diff') {
        // VS Code's native diff has search, highlighting, inline/side-by-side modes.
        const left = f.before || path.join(jobDir, 'review', 'before', f.path);
        const right = f.after || path.join(jobDir, 'review', 'after', f.path);
        for (const missing of [!f.before ? left : null, !f.after ? right : null]) {
          if (missing) { await fs.mkdir(path.dirname(missing), { recursive: true }); await fs.writeFile(missing, ''); }
        }
        await vscode.commands.executeCommand('vscode.diff', vscode.Uri.file(left), vscode.Uri.file(right), `Zipdeck: ${f.path} (${f.status})`, { preview: false });
      } else {
        const confirm = await vscode.window.showWarningMessage(`Apply Zipdeck's proposed ${f.status} file ${f.path}?`, { modal: true }, 'Apply file');
        if (confirm !== 'Apply file') return;
        await new Promise((resolve, reject) => { const proc = spawn(executable, ['--apply', jobDir, '--apply-file', f.path], { shell: false }); let err = ''; proc.stderr.on('data', b => err += b); proc.on('error', reject); proc.on('close', code => code === 0 ? resolve() : reject(new Error(err || `exit ${code}`))); });
        output.appendLine(`Applied ${f.path}`);
        vscode.window.showInformationMessage(`Zipdeck applied ${f.path}`);
      }
    } catch (e) { vscode.window.showErrorMessage(`Zipdeck: ${e.message}`); }
  });
  return review;
}
module.exports = { showReview, escapeHtml };
