import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {writeFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';
export const WINDOWS_FILES=Object.freeze(['direct_link.go','direct_link_windows.go','directory_guard_windows.go','directory_windows.go','doc.go','entries.go','entries_windows.go','path.go','probe_windows.go','root_path.go','root_windows.go']);
const foundation='agent/internal/nativefs/windowsprobe';
export function validateProductionGraph(packages,env){
 assert(['windows','darwin','linux'].includes(env.GOOS),'unsupported production graph target');
 assert.equal(env.GOFLAGS,'','ordinary production qualification requires empty effective GOFLAGS; tagged graphs are research only');
 assert(Array.isArray(packages)&&packages.length>0,'empty dependency graph');
 const seen=new Set();let commands=0,foundations=0;
 for(const p of packages){
  assert(p&&typeof p.ImportPath==='string'&&!p.Error&&!p.Incomplete&&!(p.DepsErrors??[]).length,'invalid/incomplete package');
  assert(!seen.has(p.ImportPath),'duplicate package');seen.add(p.ImportPath);
  if(p.ImportPath.startsWith('agent/cmd/')&&p.DepOnly!==true&&p.Name==='main'&&(p.Match??[]).includes('./cmd/...'))commands++;
  assert(!p.ImportPath.startsWith('agent/scripts/'),'experimental script consumer reached command');
  if((p.ImportPath==='agent/internal/nativefs'||p.ImportPath.startsWith('agent/internal/nativefs/'))){
   assert.equal(env.GOOS,'windows','nativefs not allowed on this target');assert.equal(p.ImportPath,foundation,'unapproved nativefs package');foundations++;
   assert.deepEqual([...p.GoFiles??[]].sort(),[...WINDOWS_FILES].sort(),'foundation source selection changed');
   for(const key of ['CgoFiles','CFiles','CXXFiles','MFiles','HFiles','FFiles','SFiles','SysoFiles'])assert.equal((p[key]??[]).length,0,`unexpected foundation ${key}`);
  }
  for(const file of [...p.GoFiles??[],...p.CgoFiles??[]])assert(!/nativeprobe/i.test(file)&&!(p.ImportPath.startsWith('agent/')&&/experimental/i.test(file)),`experimental source selected: ${p.ImportPath}/${file}`);
  if((p.Imports??[]).includes(foundation))assert.equal(p.ImportPath,'agent/internal/interaction','foundation must only enter through interaction adapter');
 }
 assert(commands>0,'no actual command roots');assert.equal(foundations,env.GOOS==='windows'?1:0,'unexpected foundation count');
 return {target:env.GOOS,commands,packages:packages.length,foundations};
}
// go list emits concatenated JSON objects, not JSONL. Reject incomplete output.
export function parseGoList(text){
 const result=[];let start=-1,depth=0,quoted=false,escaped=false;
 for(let i=0;i<text.length;i++){
  const c=text[i];if(start<0){if(/\s/.test(c))continue;assert.equal(c,'{','invalid graph stream');start=i;depth=1;continue;}
  if(quoted){if(escaped)escaped=false;else if(c==='\\')escaped=true;else if(c==='"')quoted=false;continue;}
  if(c==='"')quoted=true;else if(c==='{')depth++;else if(c==='}'&&--depth===0){result.push(JSON.parse(text.slice(start,i+1)));start=-1;}
 }
 assert.equal(start,-1,'truncated graph stream');assert(result.length>0,'empty graph stream');return result;
}
function go(args){const r=spawnSync('go',args,{encoding:'utf8',maxBuffer:64*1024*1024});assert(!r.error&&!r.signal&&r.status===0,`go ${args.join(' ')} failed: ${r.error??r.stderr}`);return r.stdout;}
export function checkProduction(){
 const env=JSON.parse(go(['env','-json','GOOS','GOARCH','GOFLAGS']));
 assert.equal(env.GOFLAGS,'','clear inherited GOFLAGS before ordinary release qualification; do not classify tagged graphs as production');
 const raw=go(['list','-mod=readonly','-deps','-json','./cmd/...']);
 writeFileSync('production-native-deps.json',raw);writeFileSync('production-native-env.json',JSON.stringify(env,null,2));
 const result=validateProductionGraph(parseGoList(raw),env);console.log(JSON.stringify(result));return result;
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href)checkProduction();
