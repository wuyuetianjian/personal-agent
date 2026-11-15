import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {fileURLToPath,pathToFileURL} from 'node:url';
import {spawnSync,spawn} from 'node:child_process';
import {parseGoList} from './production-native-deps.mjs';
export const ROOT=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
export function validateTarget(target){assert(['darwin-arm64','darwin-amd64','windows-amd64','windows-arm64'].includes(target),'unsupported preview target');return target;}
export function validateVersion(version){assert(/^0\.0\.0-preview\.[a-f0-9]{7,40}$/.test(version),'preview version required');return version;}
export async function reserveOutput(out){await fs.mkdir(out,{recursive:false});}
const hash=b=>createHash('sha256').update(b).digest('hex');
export async function inventory(root,rel=''){
 const result=[];
 for(const entry of (await fs.readdir(path.join(root,rel),{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name,'en'))){
  const name=entry.name,p=rel?`${rel}/${name}`:name;
  assert(!entry.isSymbolicLink()&& (entry.isFile()||entry.isDirectory()),`nonregular payload: ${p}`);
  if(p==='MANIFEST.json')continue;
  assert(!['.git','.env'].includes(name)&&!/^\.env\./.test(name)&&! /\.(?:key|pem|db|sqlite|sqlite3|node|dll|so|dylib)$/i.test(name),`forbidden payload: ${p}`);
  if(entry.isDirectory())result.push(...await inventory(root,p));
  else result.push({path:p,size:(await fs.stat(path.join(root,p))).size,sha256:hash(await fs.readFile(path.join(root,p)))});
 }
 return result;
}
export async function verifyManifest(root){
 assert((await fs.lstat(path.join(root,'MANIFEST.json'))).isFile(),'manifest must be a regular file');
 const m=JSON.parse(await fs.readFile(path.join(root,'MANIFEST.json'),'utf8'));
 assert.deepEqual(await inventory(root),m.files,'extracted payload differs from manifest');return m;
}
export function run(command,args,{cwd=ROOT,env=process.env,capture=false}={}){
 if(command==='pnpm'&&process.platform==='win32'){assert(process.env.PNPM_CLI&&path.isAbsolute(process.env.PNPM_CLI),'PNPM_CLI must identify installed official pnpm entry');args=[process.env.PNPM_CLI,...args];command=process.execPath;}
 const r=spawnSync(command,args,{cwd,env,encoding:'utf8',stdio:capture?'pipe':'inherit',maxBuffer:32*1024*1024,timeout:15*60*1000,shell:false});
 assert(!r.error&&!r.signal&&r.status===0,`${command} failed (${r.status}): ${r.error??r.stderr??''}`);return r.stdout?.trim();
}
export function startPreviewChild(exe,args,{cwd,env}={}){
 const p=spawn(exe,args,{cwd,env,stdio:['ignore','pipe','pipe']});let text='',error=null,closed=false;
 const done=new Promise(resolve=>p.once('close',(code,signal)=>{closed=true;resolve({code,signal});}));
 p.stdout.on('data',b=>{text+=b;});p.stderr.on('data',b=>{text+=b;});p.on('error',e=>{error=e;});
 return {p,done,output:()=>text,isClosed:()=>closed,async until(pattern){
  for(let i=0;i<400;i++){if(error)throw error;const m=text.match(pattern);if(m)return m;if(closed)throw Error(`child closed: ${text}`);await new Promise(r=>setTimeout(r,25));}
  throw Error(`child readiness timeout: ${text}`);
 }};
}
export async function stopPreviewChild(child,timeoutMs=3000){
 async function wait(){let timer;try{return await Promise.race([child.done.then(()=>true),new Promise(r=>{timer=setTimeout(()=>r(false),timeoutMs);})]);}finally{clearTimeout(timer);}}
 if(child.isClosed())return;
 child.p.kill();if(await wait())return;
 child.p.kill('SIGKILL');assert(await wait(),'child did not close after bounded forced shutdown');
}
export async function cleanupPreview(actions){
 const failures=[];
 for(const action of actions){try{await action();}catch(e){failures.push(e);}}
 if(failures.length)throw new AggregateError(failures,'preview cleanup failed');
}
export function fetchPreview(url,options={},timeoutMs=5000){return fetch(url,{...options,signal:AbortSignal.timeout(timeoutMs)});}
async function resolveInstalled(start,name){
 assert(/^(@[\w.-]+\/)?[\w.-]+$/.test(name),'invalid package name');
 for(let d=start;;d=path.dirname(d)){
  const p=path.join(d,'node_modules',name);
  try {return await fs.realpath(p);}catch(e){if(e.code!=='ENOENT')throw e;}
  if(path.dirname(d)===d)return null;
 }
}
// Dereferencing pnpm's top-level link alone breaks its dependency resolution.
// Materialize each exact resolved package and its own dependencies instead.
export async function materializePackage(source,dest,ancestors=[],singletons={}){
 source=await fs.realpath(source);assert(!ancestors.includes(source),'cyclic production dependency requires explicit packaging support');
 const manifest=JSON.parse(await fs.readFile(path.join(source,'package.json'),'utf8'));
 assert(!manifest.os&&!manifest.cpu&&!manifest.libc,`target-specific runtime dependency: ${manifest.name}`);
 await fs.mkdir(dest,{recursive:true});
 await fs.cp(source,dest,{recursive:true,dereference:true,filter:src=>!path.relative(source,src).split(path.sep).includes('node_modules')});
 const required=manifest.dependencies??{},optional=manifest.optionalDependencies??{},peers=manifest.peerDependencies??{};
 for(const name of [...new Set([...Object.keys(required),...Object.keys(optional),...Object.keys(peers)])].sort()){
  const resolved=await resolveInstalled(source,name);
  if(!resolved){assert(!(name in required)&&(!(name in peers)||manifest.peerDependenciesMeta?.[name]?.optional),`missing runtime dependency ${name}`);continue;}
  if(name in peers && !(name in required) && !(name in optional)){assert(name==='react'||name==='@types/react',`unqualified peer singleton ${name}`);if(name==='react')assert.equal(resolved,singletons.react,'React peer does not resolve to the root singleton source');continue;}
  await materializePackage(resolved,path.join(dest,'node_modules',name),[...ancestors,source],singletons);
 }
}
export async function prepareTerminal(dest,{storeDir}={}){
 const temp=await fs.mkdtemp(path.join(os.tmpdir(),'agent-terminal-deploy-'));
 try{
  const deployed=path.join(temp,'deployment');
  run('pnpm',[...(storeDir?['--store-dir',storeDir]:[]),'--filter','@personal-agent/terminal','deploy','--legacy','--prod',deployed]);
  await fs.mkdir(dest,{recursive:true});
  const singletonReact=await resolveInstalled(deployed,'react');assert(singletonReact,'root React missing');
  for(const name of ['ink','react']){
   const source=await resolveInstalled(deployed,name);assert(source,`missing ${name}`);
   await materializePackage(source,path.join(dest,'node_modules',name),[],{react:singletonReact});
  }
  const require=createRequire(await fs.realpath(path.join(ROOT,'apps/terminal/node_modules/tsx/package.json')));
  const esbuild=await import(pathToFileURL(require.resolve('esbuild')).href);
  assert.equal(esbuild.version,'0.25.12','unexpected locked compiler version');
  await esbuild.build({entryPoints:[path.join(ROOT,'apps/terminal/src/cli.tsx')],bundle:true,platform:'node',format:'esm',target:'node24',jsx:'automatic',external:['ink','react','react/*'],outfile:path.join(dest,'cli.mjs')});
  await inventory(dest);
 }finally{await fs.rm(temp,{recursive:true,force:true});}
}
export async function collectGoLicenses(stage,env){
 const graph=parseGoList(run('go',['list','-mod=readonly','-tags=webui','-deps','-json','./cmd/pachat'],{env,capture:true}));
 const mods=[...new Map(graph.filter(p=>p.Module&&!p.Module.Main).map(p=>[p.Module.Path,p.Module])).values()];
 const root=run('go',['env','GOROOT'],{capture:true});
 mods.push({Path:'Go-toolchain',Version:run('go',['version'],{capture:true}),Dir:root});
 const records=[];
 for(const [i,m] of mods.entries()){
  assert(!m.Replace,'replaced module not qualified for preview packaging');
  const names=(await fs.readdir(m.Dir,{withFileTypes:true})).filter(e=>e.isFile()&&/^(LICENSE|COPYING|NOTICE)(\.|$)/i.test(e.name)).map(e=>e.name);
  assert(names.length,`missing original license/notice: ${m.Path}`);
  const dest=path.join(stage,'THIRD-PARTY',String(i));await fs.mkdir(dest,{recursive:true});
  for(const name of names)await fs.copyFile(path.join(m.Dir,name),path.join(dest,name));
  records.push({module:m.Path,version:m.Version,files:names.map(n=>`THIRD-PARTY/${i}/${n}`)});
 }
 await fs.writeFile(path.join(stage,'THIRD-PARTY','go-modules.json'),JSON.stringify(records,null,2)+'\n');
}
export async function buildPreview({target,out,storeDir,localSmoke=false}){
 if(localSmoke)assert.equal(target,`${process.platform}-${process.arch==='x64'?'amd64':process.arch}`);else validateTarget(target);
 const sourceDirty=!!run('git',['status','--porcelain'],{capture:true});
 const commit=run('git',['rev-parse','HEAD'],{capture:true});const version=validateVersion(`0.0.0-preview.${commit.slice(0,12)}`);
 const [goos,goarch]=target.split('-');const env={...process.env,GOOS:goos,GOARCH:goarch,CGO_ENABLED:'0'};
 assert.equal(run('go',['env','GOFLAGS'],{capture:true}),'','inherited Go flags prohibited');
 await reserveOutput(out);
 const name=`personal-agent-${version}-${target}`;const stage=path.join(out,name);await fs.mkdir(stage);
 try{
  run('node',['scripts/production-native-deps.mjs'],{env});
  run(process.execPath,[path.join(ROOT,'apps/web/node_modules/vite/bin/vite.js'),'build'],{cwd:path.join(ROOT,'apps/web')});
  const assets=path.join(ROOT,'apps/web/dist');assert((await fs.readFile(path.join(assets,'index.html'),'utf8')).includes('/assets/'),'real Web assets required');
  const embedded=path.join(ROOT,'internal/webui/dist');await fs.mkdir(embedded,{recursive:true});for(const name of await fs.readdir(embedded)){if(name!=='placeholder.txt')await fs.rm(path.join(embedded,name),{recursive:true,force:true});}await fs.cp(assets,embedded,{recursive:true});
  const exe=goos==='windows'?'pachat.exe':'pachat';
  run('go',['build','-mod=readonly','-tags=webui','-trimpath','-ldflags',`-s -w -X agent/internal/version.Version=${version} -X agent/internal/version.Commit=${commit}`,'-o',path.join(stage,exe),'./cmd/pachat'],{env});
  await prepareTerminal(path.join(stage,'terminal'),{storeDir});
  await collectGoLicenses(stage,env);
  await fs.copyFile(path.join(ROOT,'configs/config.example.yaml'),path.join(stage,'config.example.yaml'));
  await fs.copyFile(path.join(ROOT,'docs/release/preview-start.zh-CN.md'),path.join(stage,'START-HERE.zh-CN.md'));
  await fs.writeFile(path.join(stage,'MANIFEST.json'),JSON.stringify({kind:'artifact-only-preview',version,commit,sourceDirty,target,node:'24',buildHost:`${process.platform}-${process.arch}`,nativeExecution:'not asserted by builder',permissions:'unchanged; explicit trusted Read/Search only on Mac/Windows',lockSHA256:hash(await fs.readFile(path.join(ROOT,'pnpm-lock.yaml'))),files:await inventory(stage)},null,2)+'\n');
  await verifyManifest(stage);
  const archive=path.join(out,`${name}.tar.gz`);run('tar',['-czf',archive,'-C',out,name]);
  await fs.writeFile(`${archive}.sha256`,`${hash(await fs.readFile(archive))}  ${path.basename(archive)}\n`);
  return {stage,archive,version,target};
 }catch(e){await fs.writeFile(path.join(out,'BUILD-FAILED.txt'),String(e));throw e;}
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
 const [target,out]=process.argv.slice(2);assert(target&&out,'usage: node scripts/build-preview.mjs <target> <fresh-output-dir>');
 console.log(JSON.stringify(await buildPreview({target,out:path.resolve(out),storeDir:process.env.PREVIEW_PNPM_STORE,localSmoke:process.env.PREVIEW_LOCAL_SMOKE==='1'})));
}
