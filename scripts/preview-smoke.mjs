// Operates only on freshly built owned preview archives and synthetic fixtures.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import http from 'node:http';
import {randomBytes,createHash} from 'node:crypto';
import {ROOT,run,verifyManifest,startPreviewChild,stopPreviewChild,cleanupPreview,fetchPreview,validatePreviewSource,runTar,parsePreviewArchiveList} from './build-preview.mjs';
const [archiveArg,fixtureArg,mode='native',localFlag,...extra]=process.argv.slice(2);
assert(['native','verify-only'].includes(mode),'unknown smoke mode');
assert(extra.length===0&&(localFlag===undefined||localFlag==='--allow-dirty-local'),'unknown smoke option');
const localOnly=localFlag==='--allow-dirty-local';assert(archiveArg&&fixtureArg,'archive and separately built fixture required');
const archive=path.resolve(archiveArg),fixture=path.resolve(fixtureArg);
const digest=createHash('sha256').update(await fs.readFile(archive)).digest('hex');
assert.equal((await fs.readFile(`${archive}.sha256`,'utf8')).trim(),`${digest}  ${path.basename(archive)}`);
const temp=await fs.mkdtemp(path.join(os.tmpdir(),'preview extracted smoke '));
const children=[];let attempted=0;
const guard=http.createServer((req,res)=>{attempted++;res.writeHead(500);res.end('no model calls permitted');});
function launch(exe,args,env){const child=startPreviewChild(exe,args,{cwd:temp,env});children.push(child);return child;}

try{
 const root=parsePreviewArchiveList(runTar(['-tzf',archive],{capture:true,raw:true}));
 runTar(['-xzf',archive,'-C',temp]);const unpacked=path.join(temp,root);const m=await verifyManifest(unpacked);
 validatePreviewSource(m,{allowDirtyLocal:localOnly,ci:!!(process.env.GITHUB_ACTIONS||process.env.CI)});
 const host=`${process.platform==='win32'?'windows':process.platform}-${process.arch==='x64'?'amd64':process.arch}`;
 if(mode==='verify-only'){assert.notEqual(m.target,host,'native target must run, not bypass smoke');console.log(JSON.stringify({target:m.target,sourceDirty:m.sourceDirty,localOnly,result:'archive-verified-only',nativeExecution:false}));}
 else{
  assert.equal(m.target,host,'cannot call a cross-build native verification');
  const exe=path.join(unpacked,process.platform==='win32'?'pachat.exe':'pachat');
  assert(run(exe,['version'],{cwd:temp,capture:true}).includes(`version=${m.version}`));
  const token=randomBytes(24).toString('hex');const env={...process.env,PA_AGENT_TOKEN:token,DEV:'false'};delete env.NODE_PATH;delete env.NODE_OPTIONS;
  await new Promise(r=>guard.listen(0,'127.0.0.1',r));const endpoint=`http://127.0.0.1:${guard.address().port}/v1`;
  const cfg=path.join(temp,'smoke.yaml');await fs.writeFile(cfg,(await fs.readFile(path.join(unpacked,'config.example.yaml'),'utf8')).replace('http://127.0.0.1:11434/v1',endpoint));
  const service=launch(exe,['serve-agent','--config',cfg,'--model','local-planner','--runtime-parent',path.join(temp,'runtime'),'--addr','127.0.0.1:0'],env);
  const url=(await service.until(/rebuilt preview API: (http:\/\/127\.0\.0\.1:\d+)/))[1];
  const response=await fetchPreview(url);assert.equal(response.status,200);const html=await response.text();const asset=html.match(/(?:src|href)="(\/assets\/[^\"]+\.js)"/);assert(asset,'embedded real TS Web missing');assert.equal((await fetchPreview(url+asset[1])).status,200);
  const status=await fetchPreview(url+'/api/v1/status',{headers:{Authorization:`Bearer ${token}`,'X-Agent-Protocol':'personal-agent-rebuild-v1'}});assert.equal(status.status,200);assert.equal((await status.json()).protocol,'personal-agent-rebuild-v1');assert.equal(attempted,0,'startup contacted model');
  const synthetic=launch(fixture,['--chat-only-fixture'],env);const fixtureInfo=JSON.parse((await synthetic.until(/(\{"[^\n]+\})/))[1]);
  const cli=launch(process.execPath,[path.join(unpacked,'terminal/cli.mjs'),'--url',fixtureInfo.url,'--mode','chat_only','--print','你好','--format','stream-json'],env);
  const exit=await Promise.race([cli.done,new Promise((_,rej)=>setTimeout(()=>rej(Error('CLI timeout')),30000).unref())]);assert.equal(exit.code,0,cli.output());
  const lines=cli.output().trim().split('\n').map(s=>JSON.parse(s));assert(lines.length>1&&lines.some(e=>e.type==='model.text_delta'),'no actual streaming deltas');assert.equal(lines.at(-1).status,'completed','stream did not finish');assert(cli.output().includes('真实流式测试完成'),'missing synthetic streamed answer');
  if(process.platform!=='win32')run('python3',[path.join(ROOT,'scripts/preview-pty.py'),path.join(unpacked,'terminal/cli.mjs'),fixtureInfo.url],{cwd:temp,env});
  assert.equal(attempted,0,'service startup invoked provider');console.log(JSON.stringify({target:m.target,sourceDirty:m.sourceDirty,localOnly,result:'native-unpacked-service-web-status-cli-SSE',pty:process.platform!=='win32',provider:'synthetic-only',windowsConPTY:'not-tested'}));
 }
}finally{
 await cleanupPreview([...children.map(child=>()=>stopPreviewChild(child)),async()=>{guard.closeAllConnections();await new Promise(r=>guard.close(r));},()=>fs.rm(temp,{recursive:true,force:true})]);
}
