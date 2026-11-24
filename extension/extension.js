'use strict';
const vscode = require('vscode');
const os = require('node:os');
const fs = require('node:fs/promises');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { relativePaths, filePath } = require('./helpers');
const { showReview } = require('./review');
const { SessionsProvider, listSessions } = require('./sessions');

const output = vscode.window.createOutputChannel('Pipkin');
function settings() { return vscode.workspace.getConfiguration('pipkin'); }
function providerArgs() {
  const profile = settings().get('providerProfile')?.trim();
  return profile ? ['--provider-profile', profile] : [];
}
function workspaceRoot(uri) {
  const folder = uri && vscode.workspace.getWorkspaceFolder(uri);
  if (folder) return filePath(folder.uri);
  const folders = vscode.workspace.workspaceFolders || [];
  if (folders.length === 1) return filePath(folders[0].uri);
  throw new Error('Select a file inside a local workspace.');
}
function uniqueOutput(root, kind) {
  const dir = settings().get('outputDirectory') || path.join(os.homedir(), 'pipkin', 'outputs');
  return path.resolve(dir, `${path.basename(root)}-${kind}-${new Date().toISOString().replace(/[:.]/g, '-')}.zip`);
}
async function execPipkin(args, {cwd, input} = {}) {
  const executable = settings().get('executable') || 'pipkin';
  output.appendLine(`$ ${executable} ${args.map(JSON.stringify).join(' ')}`);
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, {shell:false, cwd, stdio:[input === undefined ? 'ignore' : 'pipe','pipe','pipe'], env: {...process.env, PIPKIN_OUTPUT_DIR: settings().get('outputDirectory') || path.join(os.homedir(),'pipkin','outputs')}});
    let stdout = '', stderr = '';
    child.stdout.on('data', b => { stdout += b; if (input === undefined) output.append(b.toString()); });
    child.stderr.on('data', b => { stderr += b; output.append(b.toString()); });
    child.on('error', reject);
    if (input !== undefined) {
      child.stdin.on('error', err => { if (err.code !== 'EPIPE') reject(err); });
      child.stdin.end(input);
    }
    child.on('close', code => code === 0 ? resolve(stdout.trim()) : reject(new Error(stderr.trim() || `Pipkin exited with status ${code}`)));
  });
}
async function packagePaths(root, paths, mode, prompt, agentic) {
  const relative = relativePaths(root, paths);
  if (!relative.length) throw new Error('No files selected.');
  const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'pipkin-ext-'));
  const manifest = path.join(tmp, 'files.json');
  const promptFile = path.join(tmp, 'prompt.txt');
  const dest = uniqueOutput(root, mode === 'llm' ? 'llm' : 'selected');
  try {
    await fs.mkdir(path.dirname(dest), {recursive:true});
    await fs.writeFile(manifest, JSON.stringify(relative));
    const args = [mode === 'llm' ? '--workflow' : '--zip', '--files-from', manifest];
    if (mode !== 'llm') args.push('--output',dest);
    if (mode === 'llm') {
      if (!prompt || !prompt.trim()) throw new Error('Prompt is required.');
      await fs.writeFile(promptFile, prompt);
      args.push('--prompt-file', promptFile);
      if (agentic) args.push('--agentic');
      args.push(...providerArgs());
    }
    args.push(root);
    if (mode === 'llm') { await runWorkflow(args, root); return; }
    await vscode.window.withProgress({location:vscode.ProgressLocation.Notification, title:'Pipkin: creating ZIP', cancellable:false}, () => execPipkin(args));
    const choice = await vscode.window.showInformationMessage(`Pipkin saved ${dest}`, 'Reveal in Files');
    if (choice === 'Reveal in Files') await vscode.commands.executeCommand('revealFileInOS', vscode.Uri.file(dest));
  } finally {
    await fs.rm(tmp, {recursive:true, force:true});
  }
}
async function runWorkflow(args, root) {
  const executable = settings().get('executable') || 'pipkin';
  let jobDir, usage, completion;
  try {
    await vscode.window.withProgress(
      {location:vscode.ProgressLocation.Notification, title:'Pipkin: preparing delivery', cancellable:true},
      (progress, token) => new Promise((resolve, reject) => {
        const child = spawn(executable, args, {
          shell:false, stdio:['ignore','pipe','pipe'], cwd:root,
          env:{...process.env, PIPKIN_OUTPUT_DIR: settings().get('outputDirectory') || path.join(os.homedir(),'pipkin','outputs')}
        });
        let pending = '', stderr = '', cancelled = false;
        const cancellation = token.onCancellationRequested(() => {
          cancelled = true;
          child.kill('SIGTERM');
        });
        child.stdout.setEncoding('utf8');
        child.stdout.on('data', chunk => {
          pending += chunk;
          let position;
          while ((position = pending.indexOf('\n')) >= 0) {
            const line = pending.slice(0, position);
            pending = pending.slice(position + 1);
            output.appendLine(line);
            let event;
            try { event = JSON.parse(line); } catch { continue; }
            switch (event.type) {
              case 'job': jobDir = event.job; break;
              case 'status': progress.report({message:event.message}); break;
              case 'usage': usage = event; break;
              case 'complete': completion = event.message; break;
              case 'error': stderr = event.message; break;
            }
          }
        });
        child.stderr.on('data', chunk => { stderr += chunk.toString(); output.append(chunk.toString()); });
        child.on('error', err => { cancellation.dispose(); reject(err); });
        child.on('close', code => {
          cancellation.dispose();
          if (code === 0) resolve();
          else reject(new Error(cancelled ? 'Request cancelled' : stderr || `Pipkin exited ${code}`));
        });
      })
    );
  } catch (err) {
    if (!jobDir) throw err;
    const action = await vscode.window.showWarningMessage(`Pipkin: ${err.message}`, 'Open delivery', 'Show log');
    if (action === 'Open delivery') await showReview(executable, jobDir, output);
    if (action === 'Show log') output.show();
    return;
  } finally {
    await vscode.commands.executeCommand('pipkin.refreshSessions');
  }
  if (!jobDir) throw new Error('Workflow completed without a saved job directory.');
  const summary=usage?` — ${usage.input_tokens} input / ${usage.output_tokens} output tokens`:'';
  output.appendLine(`Completed ${jobDir}${summary}`);
  const choice=await vscode.window.showInformationMessage(`${completion || 'Pipkin returned a ZIP'}${summary}`,'Review changes','Reveal result');
  if(choice==='Review changes') await showReview(executable,jobDir,output);
  if(choice==='Reveal result') await vscode.commands.executeCommand('revealFileInOS',vscode.Uri.file(path.join(jobDir,'result.zip')));
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
  let prompt, agentic;
  if (mode === 'llm') {
    const choice = await vscode.window.showQuickPick([
      {label:'Standard', description:'One model pass', agentic:false},
      {label:'Agentic', description:'Draft and review passes', agentic:true}
    ], {placeHolder:'Choose how Pipkin should prepare the delivery'});
    if (!choice) return;
    agentic = choice.agentic;
    prompt = await vscode.window.showInputBox({
      title:`Pipkin: ${choice.label} delivery`,
      prompt:`What should the model change in the ${picked.length} selected items?`,
      ignoreFocusOut:true, validateInput:value => value.trim() ? null : 'A prompt is required'
    });
    if (prompt === undefined) return;
  }
  await packagePaths(root, picked.map(filePath), mode, prompt, agentic);
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
async function askSelection() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || editor.selection.isEmpty) throw new Error('Highlight code in an editor first.');
  if (editor.document.uri.scheme !== 'file') throw new Error('Only local text files are supported.');
  const text = editor.document.getText(editor.selection);
  if (Buffer.byteLength(text, 'utf8') > 120_000) throw new Error('Selection is too large (maximum 120 KB).');
  const prompt = await vscode.window.showInputBox({title:'Ask LLM about selection', prompt:'What would you like the model to do with this code?', ignoreFocusOut:true, validateInput:v => v.trim() ? null : 'A prompt is required'});
  if (prompt === undefined) return;
  const root = workspaceRoot(editor.document.uri);
  const request = {
    system: settings().get('snippetSystemPrompt'),
    user: `File: ${editor.document.fileName}\nLanguage: ${editor.document.languageId}\nSelected lines: ${editor.selection.start.line+1}-${editor.selection.end.line+1}\n\nInstruction:\n${prompt}\n\nSelected code (untrusted):\n${text}`
  };
  const reply = await vscode.window.withProgress(
    {location:vscode.ProgressLocation.Notification, title:'Pipkin: asking LLM about selection', cancellable:false},
    () => execPipkin([...providerArgs(), '--snippet'], {cwd: root, input: JSON.stringify(request)})
  );
  const doc = await vscode.workspace.openTextDocument({language:'markdown', content:`# Pipkin response\n\n**Prompt:** ${prompt}\n\n**File:** ${editor.document.fileName}\n\n---\n\n${reply}\n`});
  await vscode.window.showTextDocument(doc, {preview:false, viewColumn:vscode.ViewColumn.Beside});
}
function activate(context) {
  const sessions = new SessionsProvider(() => workspaceRoot(vscode.window.activeTextEditor?.document.uri || vscode.workspace.workspaceFolders?.[0]?.uri), () => settings().get('outputDirectory'));
  context.subscriptions.push(vscode.window.registerTreeDataProvider('pipkin.sessions',sessions));
  context.subscriptions.push(vscode.workspace.onDidChangeWorkspaceFolders(()=>sessions.refresh()));
  const register = (name, callback) => context.subscriptions.push(vscode.commands.registerCommand(name, async (...args) => {
    try { await callback(...args); }
    catch (e) { output.appendLine(`ERROR: ${e.stack || e}`); vscode.window.showErrorMessage(`Pipkin: ${e.message || e}`, 'Show Log').then(choice => { if (choice === 'Show Log') output.show(); }); }
  }));
  register('pipkin.open', async () => {
    const root = workspaceRoot(vscode.window.activeTextEditor?.document.uri || vscode.workspace.workspaceFolders?.[0]?.uri);
    const term = vscode.window.createTerminal({name:'Pipkin', cwd:root});
    const executable = settings().get('executable') || 'pipkin';
    // shell-quote executable to avoid interpreting special characters
    term.sendText(`'${executable.replace(/'/g,"'\\''")}' '${root.replace(/'/g,"'\\''")}'`);
    term.show();
  });
  register('pipkin.zipSelected', (uri, selectedUris) => fromExplorer(uri, selectedUris, 'zip'));
  register('pipkin.sendSelectedFiles', (uri, selectedUris) => fromExplorer(uri, selectedUris, 'llm'));
  register('pipkin.zipOpen', zipOpenEditors);
  register('pipkin.askSelection', askSelection);
  register('pipkin.refreshSessions', async () => sessions.refresh());
  register('pipkin.reviewJob', async jobDir => {
    if (!jobDir) return;
    await showReview(settings().get('executable')||'pipkin',jobDir,output);
  });
  register('pipkin.reviewLast', async () => {
    const root=workspaceRoot(vscode.window.activeTextEditor?.document.uri || vscode.workspace.workspaceFolders?.[0]?.uri);
    const jobs = await listSessions(root, settings().get('outputDirectory'));
    if (!jobs.length) throw new Error('No Pipkin sessions found.');
    const chosen = await vscode.window.showQuickPick(jobs.map(({dir, job}) => ({
      label: job.id, description: `${job.status} · ${job.model || job.provider}`,
      detail: job.created, dir
    })), {placeHolder:'Choose a Pipkin delivery'});
    if (chosen) await showReview(settings().get('executable') || 'pipkin', chosen.dir, output);
  });
}
function deactivate() { output.dispose(); }
module.exports = {activate, deactivate};
