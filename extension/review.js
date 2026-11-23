'use strict';
const vscode = require('vscode');
const path = require('node:path');
const crypto = require('node:crypto');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');

function runJSON(executable, args) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {shell:false});
    let out = '', err = '';
    child.stdout.on('data', b => out += b);
    child.stderr.on('data', b => err += b);
    child.on('error', reject);
    child.on('close', code => {
      if (code !== 0) return reject(new Error(err || `Pipkin exit ${code}`));
      try { resolve(JSON.parse(out)); } catch (e) { reject(e); }
    });
  });
}
function escapeHtml(text) { return String(text).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function workspaceLabel(status) {
  return ({unchanged_since_submission:'Unchanged locally',matches_proposal:'Already matches',changed:'Changed locally',missing:'Missing locally',existing_file_conflict:'Existing local file',unavailable:'Cannot read local file'})[status] || status;
}
async function showReview(executable, jobDir, output) {
  const job = JSON.parse(await fs.readFile(path.join(jobDir, 'job.json'),'utf8'));
  const panel = vscode.window.createWebviewPanel('pipkinReview',`Pipkin: ${job.id}`,vscode.ViewColumn.Beside,{enableScripts:true,retainContextWhenHidden:true});
  const nonce = crypto.randomBytes(16).toString('hex');
  let files = [];
  const draw = async () => {
    const review = await runJSON(executable, ['--review', jobDir]);
    files = review.files || [];
    const badge = s => ({added:'New',deleted:'Deleted',modified:'Modified',workspace_only:'Local difference'}[s] || s);
    const rows = files.map((f,i) => {
      const status = f.workspace_status || 'unknown';
      const caution = ['changed','existing_file_conflict','unavailable','missing'].includes(status) && !(status==='missing' && f.status==='added');
      return `<article class="file"><div class="line"><b>${escapeHtml(f.path)}</b><span class="tag">${escapeHtml(badge(f.status))}</span></div><div class="secondary ${caution?'warn':''}">${escapeHtml(workspaceLabel(status))}${caution?' · verify before applying':''}</div><div class="actions"><button data-action="original" data-index="${i}">Submitted ↔ Result</button><button data-action="current" data-index="${i}">Current ↔ Result</button>${f.status==='workspace_only'?'':`<button data-action="apply" data-index="${i}" class="apply">Apply</button>`}</div></article>`;
    }).join('');
    panel.webview.html = `<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-${nonce}';"><style>body{font-family:var(--vscode-font-family);color:var(--vscode-foreground);padding:16px;line-height:1.5}h1{font-size:19px;margin-bottom:4px}h2{font-size:15px;margin-top:22px}.secondary,.sub{color:var(--vscode-descriptionForeground);font-size:12px}.warn{color:var(--vscode-editorWarning-foreground)}.stats{display:flex;gap:8px;flex-wrap:wrap;margin:15px 0}.stat{background:var(--vscode-editorWidget-background);padding:12px;border-radius:7px;flex:1;min-width:85px}.stat strong{display:block;font-size:19px}.file{border-bottom:1px solid var(--vscode-panel-border);padding:13px 0}.line{display:flex;gap:10px;align-items:center;overflow-wrap:anywhere}.tag{font-size:11px;color:var(--vscode-descriptionForeground)}button{cursor:pointer;background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground);border:0;padding:7px 10px;border-radius:4px}button.apply{background:var(--vscode-button-background);color:var(--vscode-button-foreground)}.actions{display:flex;gap:7px;flex-wrap:wrap;margin-top:8px}code{overflow-wrap:anywhere} .toolbar{display:flex;gap:8px}</style></head><body><h1>Pipkin Review</h1><p class="sub">Compare the LLM's ZIP against either the original submitted files or your current workspace. No changes are made until you approve.</p><div class="stats"><div class="stat"><strong>${files.length}</strong>Changed files</div><div class="stat"><strong>${job.input_tokens ?? '—'}</strong>Input tokens</div><div class="stat"><strong>${job.output_tokens ?? '—'}</strong>Output tokens</div><div class="stat"><strong>${job.cached_tokens ?? '—'}</strong>Cached tokens</div></div><p class="sub">${escapeHtml(job.model || 'Unknown')} · ${escapeHtml(job.id)} · ${escapeHtml(job.status || '')}</p><div class="toolbar"><button data-action="refresh">Refresh workspace comparison</button><button data-action="reveal">Show returned ZIP</button></div><h2>Proposed file changes</h2>${rows || '<p>No changes between submitted and returned ZIPs.</p>'}<p class="sub">Session: <code>${escapeHtml(jobDir)}</code></p><script nonce="${nonce}">const vscode=acquireVsCodeApi();document.addEventListener('click',ev=>{const button=ev.target.closest('button[data-action]');if(button)vscode.postMessage({action:button.dataset.action,index:Number(button.dataset.index)});});</script></body></html>`;
  };
  panel.webview.onDidReceiveMessage(async msg => {
    try {
      if (msg.action === 'refresh') {await draw();return;}
      if (msg.action === 'reveal') {await vscode.commands.executeCommand('revealFileInOS',vscode.Uri.file(path.join(jobDir,'result.zip')));return;}
      const f = files[msg.index];
      if (!f || !['original','current','apply'].includes(msg.action)) return;
      if (msg.action === 'original' || msg.action === 'current') {
        // A review re-snapshots the live workspace before opening the diff.
        if (msg.action === 'current') await draw();
        const updated = files.find(x=>x.path===f.path) || f;
        const left = msg.action === 'current' ? updated.current : updated.before;
        const right = updated.after;
        const virtualDir = path.join(jobDir,'review','empty');
        await fs.mkdir(virtualDir,{recursive:true});
        const empty = path.join(virtualDir,'empty.txt');
        await fs.writeFile(empty,'');
        await vscode.commands.executeCommand('vscode.diff',vscode.Uri.file(left || empty),vscode.Uri.file(right || empty),`Pipkin: ${f.path} — ${msg.action === 'current'?'Current vs LLM':'Submitted vs LLM'}`,{preview:false});
      } else {
        const confirm = await vscode.window.showWarningMessage(`Apply proposed ${f.status} file ${f.path}? Local conflicts are checked automatically.`,{modal:true},'Apply file');
        if (confirm !== 'Apply file') return;
        await new Promise((resolve,reject) => {const proc=spawn(executable,['--apply',jobDir,'--apply-file',f.path],{shell:false});let err='';proc.stderr.on('data',b=>err+=b);proc.on('error',reject);proc.on('close',code=>code===0?resolve():reject(new Error(err||`exit ${code}`)));});
        output.appendLine(`Applied ${f.path}`);
        await draw();
        vscode.window.showInformationMessage(`Pipkin applied ${f.path}`);
      }
    } catch(e) {vscode.window.showErrorMessage(`Pipkin: ${e.message}`);}
  });
  await draw();
}
module.exports = {showReview,escapeHtml,workspaceLabel};
