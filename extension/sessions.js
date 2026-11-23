'use strict';
const vscode = require('vscode');
const os = require('node:os');
const path = require('node:path');
const fs = require('node:fs/promises');

class SessionsProvider {
  constructor(getRoot,getOutput) {this.getRoot=getRoot;this.getOutput=getOutput;this.emitter=new vscode.EventEmitter();this.onDidChangeTreeData=this.emitter.event;}
  refresh(){this.emitter.fire();}
  getTreeItem(i){return i;}
  async getChildren(){
    let root;try{root=this.getRoot();}catch{return [];}
    const dir=path.join(this.getOutput()||path.join(os.homedir(),'pipkin','outputs'),'jobs',path.basename(root));
    let entries;try{entries=await fs.readdir(dir,{withFileTypes:true});}catch{return [];}
    const jobs=[];
    for(const e of entries.filter(x=>x.isDirectory()).sort((a,b)=>b.name.localeCompare(a.name)).slice(0,100)){
      const jobDir=path.join(dir,e.name);
      try {
        const job=JSON.parse(await fs.readFile(path.join(jobDir,'job.json'),'utf8'));
        const item=new vscode.TreeItem(`${job.id}`,vscode.TreeItemCollapsibleState.None);
        item.description=`${job.status} · ${job.model || job.provider}`;
        item.tooltip=`${job.created}\n${jobDir}`;
        item.iconPath=new vscode.ThemeIcon(job.status==='completed'?'git-compare':job.status==='failed'?'error':'loading');
        item.command={command:'pipkin.reviewJob',title:'Review Pipkin Session',arguments:[jobDir]};
        jobs.push(item);
      } catch {/* skip incomplete entries */}
    }
    return jobs;
  }
}
module.exports={SessionsProvider};
