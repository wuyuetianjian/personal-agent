import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,symlink,rm,realpath} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {validateTarget,validateVersion,reserveOutput,inventory,verifyManifest} from './build-preview.mjs';
async function fixture(t){const p=await mkdtemp(path.join(os.tmpdir(),'preview-test-'));t.after(()=>rm(p,{recursive:true,force:true}));return p;}
test('only explicit four preview targets and preview versions are packageable',()=>{
 for(const x of ['darwin-arm64','darwin-amd64','windows-amd64','windows-arm64'])assert.equal(validateTarget(x),x);
 for(const x of ['linux-amd64','../windows-amd64','windows-x64',''])assert.throws(()=>validateTarget(x));
 assert.equal(validateVersion('0.0.0-preview.a123456'),'0.0.0-preview.a123456');
 for(const x of ['1.0.0','1.0.0-release','0.0.0-preview../secret',''])assert.throws(()=>validateVersion(x));
});
test('fresh staging refuses existing output instead of mixing stale packages',async t=>{
 const p=await fixture(t);const out=path.join(p,'new');await reserveOutput(out);await assert.rejects(reserveOutput(out));
});
test('inventory binds all regular payload bytes and rejects links, secrets and native addons',async t=>{
 const p=await fixture(t);await writeFile(path.join(p,'ok.txt'),'hello');const files=await inventory(p);assert.equal(files.length,1);assert.match(files[0].sha256,/^[a-f0-9]{64}$/);assert.equal(files[0].path,'ok.txt');
 for(const name of ['.env','secret.key','runtime.db','addon.node']){await writeFile(path.join(p,name),'not shipped');await assert.rejects(inventory(p));await rm(path.join(p,name));}
 if(process.platform!=='win32'){await symlink(path.join(p,'ok.txt'),path.join(p,'alias'));await assert.rejects(inventory(p));}
});
test('verification rejects changed, missing and unexpected extracted bytes',async t=>{
 const p=await fixture(t);await writeFile(path.join(p,'a'),'one');const files=await inventory(p);await writeFile(path.join(p,'MANIFEST.json'),JSON.stringify({files}));await verifyManifest(p);
 await writeFile(path.join(p,'a'),'two');await assert.rejects(verifyManifest(p));await writeFile(path.join(p,'a'),'one');await writeFile(path.join(p,'extra'),'x');await assert.rejects(verifyManifest(p));await rm(path.join(p,'extra'));await rm(path.join(p,'a'));await assert.rejects(verifyManifest(p));
});
test('materialized peer React stays shared instead of creating a second hooks instance',async t=>{
 const {materializePackage}=await import('./build-preview.mjs');const p=await fixture(t);const nm=path.join(p,'node_modules');await mkdir(path.join(nm,'ink'),{recursive:true});await mkdir(path.join(nm,'react'));
 await writeFile(path.join(nm,'ink','package.json'),JSON.stringify({name:'ink',peerDependencies:{react:'*'}}));await writeFile(path.join(nm,'react','package.json'),JSON.stringify({name:'react'}));
 const out=path.join(p,'output','node_modules','ink');await materializePackage(path.join(nm,'ink'),out,[],{react:await realpath(path.join(nm,'react'))});
 const {access}=await import('node:fs/promises');await assert.rejects(access(path.join(out,'node_modules','react')));
 await mkdir(path.join(nm,'ink','node_modules','react'),{recursive:true});await writeFile(path.join(nm,'ink','node_modules','react','package.json'),JSON.stringify({name:'react',version:'different'}));
 await assert.rejects(materializePackage(path.join(nm,'ink'),path.join(p,'mismatch'),[],{react:await realpath(path.join(nm,'react'))}),/root singleton/);
});
test('manifest itself cannot be a link',async t=>{
 if(process.platform==='win32')return;const p=await fixture(t);const elsewhere=path.join(p,'outside');await mkdir(elsewhere);await writeFile(path.join(elsewhere,'manifest'),'{}');await symlink(path.join(elsewhere,'manifest'),path.join(p,'MANIFEST.json'));await assert.rejects(verifyManifest(p));
});
test('preview CI is artifact-only with four targets and explicit native versus cross execution',async()=>{
 const {readFile}=await import('node:fs/promises');const s=await readFile(new URL('../.github/workflows/preview-packages.yml',import.meta.url),'utf8');
 for(const target of ['darwin-arm64','darwin-amd64','windows-amd64','windows-arm64'])assert(s.includes(`target: ${target}`));
 for(const required of ['contents: read','macos-15-intel','macos-15','windows-2025','verify-only','scripts/preview-smoke.mjs','scripts/build-preview.test.mjs','--frozen-lockfile','PNPM_CLI','actions/upload-artifact@'])assert(s.includes(required),required);
 assert(s.includes('python3 scripts/preview-pty.test.py'),'PTY environment regression must run in CI');
 assert(s.includes("if: ${{ runner.os != 'Windows' }}"),'POSIX PTY regression must not be claimed on Windows');
 assert(!/contents: write|gh release|action-gh-release|git tag/.test(s));
});
test('missing child executable fails immediately and cleanup awaits bounded close, not absent exit',async t=>{
 const {startPreviewChild,stopPreviewChild}=await import('./build-preview.mjs');const p=await fixture(t);const child=startPreviewChild(path.join(p,'missing-program'),[],{cwd:p});
 await assert.rejects(child.until(/ready/),/ENOENT/);await stopPreviewChild(child,500);assert(child.isClosed());
});
test('cleanup attempts remaining resources even after the first failure',async()=>{
 const {cleanupPreview}=await import('./build-preview.mjs');const called=[];const fault=Error('first cleanup fault');
 await assert.rejects(cleanupPreview([async()=>{called.push('first');throw fault;},async()=>{called.push('second');},async()=>{called.push('third');}]),e=>e instanceof AggregateError&&e.errors.includes(fault));
 assert.deepEqual(called,['first','second','third']);
});
test('preview HTTP requests abort even when a connected server never responds',async()=>{
 const {fetchPreview}=await import('./build-preview.mjs');const {createServer}=await import('node:http');const server=createServer(()=>{});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 try{await assert.rejects(fetchPreview(`http://127.0.0.1:${server.address().port}`,{},100),/timeout|abort/i);}finally{server.closeAllConnections();await new Promise(r=>server.close(r));}
});
test('workflow logs are outside checkout before source status is captured',async()=>{
 const {readFile}=await import('node:fs/promises');const s=await readFile(new URL('../.github/workflows/preview-packages.yml',import.meta.url),'utf8');
 for(const name of ['preview-build.log','preview-smoke.log']){
  assert(s.includes(`tee "$RUNNER_TEMP/${name}"`),`tee must not dirty checkout: ${name}`);
  assert(s.includes('${{ runner.temp }}/'+name),`artifact must preserve external log: ${name}`);
 }
 assert(!s.includes('--allow-dirty-local'),'CI must not allow dirty source');
});
test('candidate source must be explicitly clean; dirty local override cannot run in CI',async()=>{
 const {validatePreviewSource}=await import('./build-preview.mjs');
 assert.doesNotThrow(()=>validatePreviewSource({sourceDirty:false}));
 for(const sourceDirty of [true,undefined,'false',null])assert.throws(()=>validatePreviewSource({sourceDirty}));
 assert.doesNotThrow(()=>validatePreviewSource({sourceDirty:true},{allowDirtyLocal:true}));
 for(const ci of [true])assert.throws(()=>validatePreviewSource({sourceDirty:true},{allowDirtyLocal:true,ci}));
 assert.throws(()=>validatePreviewSource({sourceDirty:false},{allowDirtyLocal:true,ci:true}));
});
test('Windows tar is the explicit system executable, never Git Bash PATH tar',async()=>{
 const {tarExecutable}=await import('./build-preview.mjs');
 assert.equal(tarExecutable('win32',{SystemRoot:'D:\\Windows Root',PATH:'untrusted-git-bash'}),'D:\\Windows Root\\System32\\tar.exe');
 assert.equal(tarExecutable('win32',{SYSTEMROOT:'C:\\Windows'}),'C:\\Windows\\System32\\tar.exe');
 assert.equal(tarExecutable('darwin',{}),'tar');
 for(const root of ['', 'relative', '\\\\server\\Windows'])assert.throws(()=>tarExecutable('win32',{SystemRoot:root}));
});
test('archive listing accepts LF and CRLF without trimming filename boundaries',async()=>{
 const {parsePreviewArchiveList}=await import('./build-preview.mjs');
 for(const newline of ['\n','\r\n'])assert.equal(parsePreviewArchiveList(['preview/','preview/file with spaces.txt','preview/ terminal.txt',''].join(newline)),'preview');
 for(const listing of ['preview/\nother/file\n','preview/\npreview/../secret\n','/absolute/\n','preview/\npreview\\..\\secret\n','preview/\n preview/file\n'])assert.throws(()=>parsePreviewArchiveList(listing));
});
