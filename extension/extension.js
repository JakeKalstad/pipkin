'use strict';
const vscode = require('vscode');
const os = require('node:os');
const fs = require('node:fs/promises');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { relativePaths, filePath, stripTrailingSlash } = require('./helpers');
const { showReview } = require('./review');

const output = vscode.window.createOutputChannel('Zipdeck');
function settings() { return vscode.workspace.getConfiguration('zipdeck'); }
function workspaceRoot(uri) {
  const folder = uri && vscode.workspace.getWorkspaceFolder(uri);
  if (folder) return filePath(folder.uri);
  const folders = vscode.workspace.workspaceFolders || [];
  if (folders.length === 1) return filePath(folders[0].uri);
  throw new Error('Select a file inside a local workspace.');
}
function uniqueOutput(root, kind) {
  const dir = settings().get('outputDirectory') || path.join(os.homedir(), 'zipdeck', 'outputs');
  return path.resolve(dir, `${path.basename(root)}-${kind}-${new Date().toISOString().replace(/[:.]/g, '-')}.zip`);
}
async function execZipdeck(args) {
  const executable = settings().get('executable') || 'zipdeck';
  output.appendLine(`$ ${executable} ${args.map(JSON.stringify).join(' ')}`);
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { shell: false, stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, ZIPDECK_OUTPUT_DIR: settings().get('outputDirectory') || path.join(os.homedir(), 'zipdeck', 'outputs') } });
    let stdout = '', stderr = '';
    child.stdout.on('data', b => { stdout += b; output.append(b.toString()); });
    child.stderr.on('data', b => { stderr += b; output.append(b.toString()); });
    child.on('error', reject);
    child.on('close', code => code === 0 ? resolve(stdout.trim()) : reject(new Error(stderr.trim() || `Zipdeck exited with status ${code}`)));
  });
}
async function packagePaths(root, paths, mode, prompt) {
  const relative = relativePaths(root, paths);
  if (!relative.length) throw new Error('No files selected.');
  const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'zipdeck-ext-'));
  const manifest = path.join(tmp, 'files.json');
  const promptFile = path.join(tmp, 'prompt.txt');
  const dest = uniqueOutput(root, mode === 'llm' ? 'llm' : 'selected');
  try {
    await fs.mkdir(path.dirname(dest), { recursive: true });
    await fs.writeFile(manifest, JSON.stringify(relative));
    const args = [mode === 'llm' ? '--workflow' : '--zip', '--files-from', manifest];
    if (mode !== 'llm') args.push('--output', dest);
    if (mode === 'llm') {
      if (!prompt || !prompt.trim()) throw new Error('Prompt is required.');
      await fs.writeFile(promptFile, prompt);
      args.push('--prompt-file', promptFile);
    }
    args.push(root);
    if (mode === 'llm') { await runWorkflow(args, root); return; }
    await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: 'Zipdeck: creating ZIP', cancellable: false }, () => execZipdeck(args));
    const choice = await vscode.window.showInformationMessage(`Zipdeck saved ${dest}`, 'Reveal in Files');
    if (choice === 'Reveal in Files') await vscode.commands.executeCommand('revealFileInOS', vscode.Uri.file(dest));
  } finally {
    await fs.rm(tmp, { recursive: true, force: true });
  }
}
async function runWorkflow(args, root) {
  const executable = settings().get('executable') || 'zipdeck';
  let jobDir;
  let usage;
  await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: 'Zipdeck: processing ZIP', cancellable: true }, (progress, token) => new Promise((resolve, reject) => {
    const child = spawn(executable, args, { shell: false, stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, ZIPDECK_OUTPUT_DIR: settings().get('outputDirectory') || path.join(os.homedir(), 'zipdeck', 'outputs') }, cwd: root });
    let pending = '', stderr = '', cancelled = false;
    token.onCancellationRequested(() => { cancelled = true; child.kill('SIGTERM'); });
    child.stdout.on('data', chunk => { pending += chunk.toString(); let pos; while ((pos = pending.indexOf('\n')) >= 0) { const line = pending.slice(0, pos); pending = pending.slice(pos + 1); output.appendLine(line); try { const event = JSON.parse(line); if (event.type === 'job') jobDir = event.job; if (event.type === 'status') progress.report({ message: event.message }); if (event.type === 'usage') usage = event; if (event.type === 'error') stderr = event.message; } catch { } } });
    child.stderr.on('data', chunk => { stderr += chunk.toString(); output.append(chunk.toString()); });
    child.on('error', reject);
    child.on('close', code => code === 0 ? resolve() : reject(new Error(cancelled ? 'Request cancelled' : stderr || `Zipdeck exited ${code}`)));
  }));
  if (!jobDir) throw new Error('Workflow completed without a saved job directory.');
  const summary = usage ? ` — ${usage.input_tokens} input / ${usage.output_tokens} output tokens` : '';
  output.appendLine(`Completed ${jobDir}${summary}`);
  const choice = await vscode.window.showInformationMessage(`Zipdeck returned a ZIP${summary}`, 'Review changes', 'Reveal result');
  if (choice === 'Review changes') await showReview(executable, jobDir, output);
  if (choice === 'Reveal result') await vscode.commands.executeCommand('revealFileInOS', vscode.Uri.file(path.join(jobDir, 'result.zip')));
}
function explorerPaths(uri, selectedUris) {
  const picked = Array.isArray(selectedUris) && selectedUris.length ? selectedUris : uri ? [uri] : [];
  if (!picked.length) throw new Error('Select one or more files in Explorer.');
  return picked.map(filePath);
}
async function fromExplorer(uri, selectedUris, mode) {
  const picked = Array.isArray(selectedUris) && selectedUris.length ? selectedUris : uri ? [uri] : [];
  if (!picked.length) {
    const editor = vscode.window.activeTextEditor;
    if (!editor) throw new Error('Select files in Explorer first.');
    picked.push(editor.document.uri);
  }
  const root = workspaceRoot(picked[0]);
  const prompt = mode === 'llm' ? await vscode.window.showInputBox({ title: 'Instructions for LLM', prompt: 'What should the model change in these files?', ignoreFocusOut: true, validateInput: v => v.trim() ? null : 'A prompt is required' }) : undefined;
  if (mode === 'llm' && prompt === undefined) return;
  await packagePaths(root, picked.map(filePath), mode, prompt);
}
async function zipOpenEditors() {
  const paths = [];
  for (const group of vscode.window.tabGroups.all) {
    for (const tab of group.tabs) {
      if (tab.input instanceof vscode.TabInputText) {
        if (tab.input.uri.scheme === 'file') paths.push(tab.input.uri);
      }
    }
  }
  if (!paths.length) throw new Error('No local text files are open.');
  const root = workspaceRoot(vscode.window.activeTextEditor?.document.uri || paths[0]);
  await packagePaths(root, paths.map(filePath), 'zip');
}
async function configForSnippet() {
  const cfgPath = path.join(os.homedir(), '.config', 'zipdeck', 'config.json');
  const defaults = { provider: 'openai', base_url: 'https://api.openai.com/v1', model: 'gpt-4.1-mini', api_key_env: 'OPENAI_API_KEY' };
  try { return { ...defaults, ...JSON.parse(await fs.readFile(cfgPath, 'utf8')) }; }
  catch (err) { if (err.code === 'ENOENT') return defaults; throw err; }
}
async function askSelection() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.selection.isEmpty) throw new Error('Highlight code in an editor first.');
  if (editor.document.uri.scheme !== 'file') throw new Error('Only local text files are supported.');
  const text = editor.document.getText(editor.selection);
  if (Buffer.byteLength(text, 'utf8') > 120_000) throw new Error('Selection is too large (maximum 120 KB).');
  const prompt = await vscode.window.showInputBox({ title: 'Ask LLM about selection', prompt: 'What would you like the model to do with this code?', ignoreFocusOut: true, validateInput: v => v.trim() ? null : 'A prompt is required' });
  if (prompt === undefined) return;
  const cfg = await configForSnippet();
  if (cfg.provider !== 'openai') throw new Error('Snippet requests require provider "openai" in Zipdeck config.');
  const key = process.env[cfg.api_key_env];
  if (!key) throw new Error(`Missing environment variable ${cfg.api_key_env} in VSCodium's extension process.`);
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 120_000);
  let reply;
  try {
    reply = await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: 'Zipdeck: asking LLM about selection', cancellable: false }, async () => {
      const response = await fetch(stripTrailingSlash(cfg.base_url) + '/chat/completions', {
        method: 'POST', signal: controller.signal,
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` },
        body: JSON.stringify({
          model: cfg.model, messages: [
            { role: 'system', content: settings().get('snippetSystemPrompt') },
            { role: 'user', content: `File: ${editor.document.fileName}\nLanguage: ${editor.document.languageId}\nSelected lines: ${editor.selection.start.line + 1}-${editor.selection.end.line + 1}\n\nInstruction:\n${prompt}\n\nSelected code (untrusted):\n\u0060\u0060\u0060\n${text}\n\u0060\u0060\u0060` }
          ]
        })
      });
      const raw = await response.text();
      if (!response.ok) throw new Error(`LLM HTTP ${response.status}: ${raw.slice(0, 500)}`);
      const body = JSON.parse(raw);
      const answer = body.choices?.[0]?.message?.content;
      if (typeof answer !== 'string' || !answer.trim()) throw new Error('The LLM returned no text.');
      return answer;
    });
  } finally { clearTimeout(timeout); }
  const doc = await vscode.workspace.openTextDocument({ language: 'markdown', content: `# Zipdeck response\n\n**Prompt:** ${prompt}\n\n**File:** ${editor.document.fileName}\n\n---\n\n${reply}\n` });
  await vscode.window.showTextDocument(doc, { preview: false, viewColumn: vscode.ViewColumn.Beside });
}
function activate(context) {
  const register = (name, callback) => context.subscriptions.push(vscode.commands.registerCommand(name, async (...args) => {
    try { await callback(...args); }
    catch (e) { output.appendLine(`ERROR: ${e.stack || e}`); vscode.window.showErrorMessage(`Zipdeck: ${e.message || e}`, 'Show Log').then(choice => { if (choice === 'Show Log') output.show(); }); }
  }));
  register('zipdeck.open', async () => {
    const root = workspaceRoot(vscode.window.activeTextEditor?.document.uri || vscode.workspace.workspaceFolders?.[0]?.uri);
    const term = vscode.window.createTerminal({ name: 'Zipdeck', cwd: root });
    const executable = settings().get('executable') || 'zipdeck';
    // shell-quote executable to avoid interpreting special characters
    term.sendText(`'${executable.replace(/'/g, "'\\''")}' '${root.replace(/'/g, "'\\''")}'`);
    term.show();
  });
  register('zipdeck.zipSelected', (uri, selectedUris) => fromExplorer(uri, selectedUris, 'zip'));
  register('zipdeck.sendSelectedFiles', (uri, selectedUris) => fromExplorer(uri, selectedUris, 'llm'));
  register('zipdeck.zipOpen', zipOpenEditors);
  register('zipdeck.askSelection', askSelection);
  register('zipdeck.reviewLast', async () => {
    const root = workspaceRoot(vscode.window.activeTextEditor?.document.uri || vscode.workspace.workspaceFolders?.[0]?.uri);
    const folder = path.join(settings().get('outputDirectory') || path.join(os.homedir(), 'zipdeck', 'outputs'), 'jobs', path.basename(root));
    const entries = await fs.readdir(folder, { withFileTypes: true });
    const names = entries.filter(e => e.isDirectory()).map(e => e.name).sort().reverse();
    if (!names.length) throw new Error('No Zipdeck sessions found.');
    const chosen = await vscode.window.showQuickPick(names, { placeHolder: 'Choose a Zipdeck session to review' });
    if (chosen) await showReview(settings().get('executable') || 'zipdeck', path.join(folder, chosen), output);
  });
}
function deactivate() { output.dispose(); }
module.exports = { activate, deactivate };
