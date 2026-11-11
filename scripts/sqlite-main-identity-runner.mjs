// Disconnected Windows experiment. Never edits the repository's module files or
// the Go module cache. Native source/patch copies exist only during this run.
import { createHash } from 'node:crypto';
import { StringDecoder } from 'node:string_decoder';
import { spawn } from 'node:child_process';
import { copyFile, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rm, rmdir, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const PIN = Object.freeze({
  module: 'modernc.org/sqlite', version: 'v1.58.0',
  sum: 'h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=',
  goModSum: 'h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=',
});
export const PATCH_RELATIVE = 'scripts/sqlite-main-identity/driver_patch_windows.go.txt';
const PATCH_NAME = 'nativeprobe_sqlite_identity_windows.go';
const DEFAULT_REPO = fileURLToPath(new URL('../', import.meta.url));

export function validateInputs(mod, sum, patch) {
  // This experiment supports the repository's current no-replace module form.
  // Reject all existing replacements rather than relocating relative targets.
  const clean = mod.replace(/\/\/[^\n]*/g, '');
  if (/\breplace\b/.test(clean)) throw new Error('existing replace directives are not supported by this isolated runner');
  const matches = [...clean.matchAll(/(?:^|\n)\s*(?:require\s+)?modernc\.org\/sqlite\s+(\S+)\s*(?=\n|$)/g)];
  if (matches.length !== 1 || matches[0][1] !== PIN.version) throw new Error('repository SQLite version must be exactly '+PIN.version);
  for (const [version, checksum] of [[PIN.version, PIN.sum], [PIN.version+'/go.mod', PIN.goModSum]]) {
    const lines = sum.split(/\r?\n/).map(s => s.trim().split(/\s+/)).filter(a => a[0] === PIN.module && a[1] === version);
    if (lines.length !== 1 || lines[0].length !== 3 || lines[0][2] !== checksum) throw new Error('repository go.sum pin mismatch for '+version);
  }
  if (!/^\/\/go:build windows && nativeprobe_sqlite_identity\r?\n\s*\r?\n/.test(patch) ||
      (patch.match(/^\/\/go:build /gm) ?? []).length !== 1 || !/^package sqlite\s*$/m.test(patch)) {
    throw new Error('driver patch must be package sqlite gated exactly windows && nativeprobe_sqlite_identity');
  }
}

export function testArguments(modfile) {
  return ['test', '-json', '-modfile='+modfile, '-tags=nativeprobe_sqlite_identity', '-count=3', '-timeout=10m', '-run=^TestNative(Protected|SQLite)',
    './internal/nativefs/rootlease', './internal/nativefs/windowsprobe'];
}

export const EXPECTED_TESTS = Object.freeze({
  'agent/internal/nativefs/windowsprobe': Object.freeze([
    'TestNativeProtectedObjectBeforeIOOpen', 'TestNativeProtectedNeighborAndOwnership',
    'TestNativeProtectedInputsFailClosed', 'TestNativeProtectedPartialFailureReleasesDuplicates',
  ]),
  'agent/internal/nativefs/rootlease': Object.freeze([
    'TestNativeSQLiteMainIdentityProtectedAndNeighbor', 'TestNativeSQLiteMainIdentityDuplicateCloseDoesNotCloseSQLite',
    'TestNativeSQLiteMainIdentityUnavailableInputs', 'TestNativeSQLiteMainIdentityConnectionRecreation', 'TestNativeSQLiteMainIdentityCloseLifetimes',
    'TestNativeSQLiteCurrentWALProtectedAndIdentities', 'TestNativeSQLiteCurrentWALDuplicateRightsAndOwnership',
    'TestNativeSQLiteCurrentWALMissingJournalRefusesWithoutLeak', 'TestNativeSQLiteCurrentWALCheckpointAndConnectionRecreation',
    'TestNativeSQLiteCurrentWALHandleInventoryPositiveControl',
    'TestNativeSQLitePrivateFactoryAcceptsAbsoluteFilename', 'TestNativeSQLitePrivateFactoryRejectsUntrustedDSN',
    'TestNativeSQLitePrivateFactoryEncodesLiteralFilename', 'TestNativeSQLitePrivateFactoryJournalInterfaceBoundary',
    'TestNativeSQLitePrivateFactoryDriverInterfaces', 'TestNativeSQLitePrivateFactoryOverridesGlobalSharedCache',
  ]),
});

// Keep only the fixed manifest's counters and one bounded JSON line. Output
// events, including arbitrary printed PASS text, cannot count as test results.
export function createEventValidator() {
  const decoder=new StringDecoder('utf8');
  const counts=new Map();
  for(const [pkg,names] of Object.entries(EXPECTED_TESTS)) for(const name of names) counts.set(pkg+'\0'+name,{runs:0,passes:0});
  const completed=new Set();
  let pending='', finished=false;
  const maxLine=1024*1024;
  function line(text) {
    if(text.endsWith('\r')) text=text.slice(0,-1);
    if(!text) throw new Error('empty native JSON event');
    if(Buffer.byteLength(text)>maxLine) throw new Error('native JSON event exceeds line bound');
    let event;
    try { event=JSON.parse(text); } catch { throw new Error('malformed native JSON event'); }
    if(!event || typeof event.Action!=='string') throw new Error('invalid native JSON event fields');
    if(event.Action==='skip'||event.Action==='fail'||event.Action.endsWith('-fail')) throw new Error('native event refuses '+event.Action+' '+(event.Package??event.ImportPath??'unknown')+' '+(event.Test??''));
    // Go 1.26 interleaves BuildEvents (ImportPath) with TestEvents (Package).
    if(event.Action==='build-output' && typeof event.ImportPath==='string' && typeof event.Output==='string') return;
    if(typeof event.Package!=='string') throw new Error('invalid native test Package');
    const target=Object.hasOwn(EXPECTED_TESTS,event.Package);
    if(!target) return; // Dependency build/output events cannot satisfy targets.
    if(completed.has(event.Package)) throw new Error('native events after package completion');
    if(!event.Test && event.Action==='pass') {
      for(const name of EXPECTED_TESTS[event.Package]) {
        const count=counts.get(event.Package+'\0'+name);
        if(count.runs!==3||count.passes!==3) throw new Error('native manifest incomplete at package pass: '+name);
      }
      completed.add(event.Package);
      return;
    }
    const count=counts.get(event.Package+'\0'+event.Test);
    if(!count) return;
    if(event.Action==='run') {
      if(count.runs!==count.passes||count.runs>=3) throw new Error('unexpected native test repetition');
      count.runs++;
    } else if(event.Action==='pass') {
      if(count.runs!==count.passes+1||count.passes>=3) throw new Error('unpaired/extra native pass');
      count.passes++;
    }
  }
  return {
    feed(chunk) {
      if(finished) throw new Error('native events after validator finish');
      // Split before concatenating, so retained state never contains the full
      // run; each normal child_process chunk is independently discarded.
      const pieces=decoder.write(chunk).split('\n');
      for(let i=0;i<pieces.length;i++) {
        pending+=pieces[i];
        if(Buffer.byteLength(pending)>maxLine) throw new Error('native JSON event exceeds line bound');
        if(i<pieces.length-1) { line(pending); pending=''; }
      }
    },
    finish() {
      if(finished) throw new Error('native validator already finished');
      finished=true;
      pending+=decoder.end();
      if(pending!=='') throw new Error('truncated native JSON stream');
      if(completed.size!==Object.keys(EXPECTED_TESTS).length) throw new Error('native manifest missing package passes');
    },
  };
}

export async function copyModule(source, target, onCreated=()=>{}) {
  const info = await lstat(source);
  if (info.isSymbolicLink()) throw new Error('module symbolic links are not allowed');
  if (!info.isDirectory()) throw new Error('module source must be a directory');
  await mkdir(target, { recursive: false, mode: 0o700 });
  onCreated(); // Ownership begins only after this exact directory was created.
  for (const item of await readdir(source, {withFileTypes:true})) {
    if (item.name === 'testdata' || item.name === '.git') continue;
    const from=path.join(source,item.name), to=path.join(target,item.name);
    if (item.isSymbolicLink()) throw new Error('module symlink refused: '+item.name);
    if (item.isDirectory()) await copyModule(from,to);
    else if (item.isFile()) await copyFile(from,to);
    else throw new Error('unsupported module filesystem entry: '+item.name);
  }
}

function within(parent, child) {
  const relative=path.relative(parent,child);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith('..'+path.sep));
}

// stdout/stderr are streamed in full. Captured stdout is used only for small Go
// metadata responses; test output is never accumulated, filtered or truncated.
function executeGo(args, options) {
  return new Promise((resolve,reject)=>{
    const capture = args[0] !== 'test';
    const child=spawn('go',args,{cwd:options.cwd,env:options.env,shell:false,stdio:['ignore','pipe','pipe']});
    let stdout='', overflow=false, validationError; 
    child.stdout.on('data',chunk=>{
      process.stdout.write(chunk);
      if(!capture && !validationError) {
        try { options.onStdout?.(chunk); } catch(error) { validationError=error; }
      }
      if (capture && !overflow) {
        stdout+=chunk.toString();
        if (Buffer.byteLength(stdout)>8*1024*1024) { overflow=true; child.kill(); }
      }
    });
    child.stderr.on('data',chunk=>process.stderr.write(chunk));
    child.once('error',reject);
    child.once('close',(code,signal)=>{
      if (overflow) reject(new Error('Go metadata exceeded capture bound; full output was streamed'));
      else if(code!==0) reject(new Error('go '+args.join(' ')+' failed: exit='+code+' signal='+signal));
      else if(validationError) reject(validationError);
      else resolve(stdout);
    });
  });
}

export async function runExperiment({platform=process.platform,repo=DEFAULT_REPO,tempRoot=os.tmpdir(),command=executeGo,log=console.log}={}) {
  if(platform!=='win32') throw new Error('Windows-only native SQLite experiment; refusing rather than skipping');
  repo=await realpath(repo);
  const [mod,sum,patch]=await Promise.all(['go.mod','go.sum',PATCH_RELATIVE].map(name=>readFile(path.join(repo,name),'utf8')));
  validateInputs(mod,sum,patch);
  log('Experimental source: '+PIN.module+' '+PIN.version+' sum='+PIN.sum+' goModSum='+PIN.goModSum);
  log('Experimental patch SHA256='+createHash('sha256').update(patch).digest('hex'));
  const options={cwd:repo,env:{...process.env,GOWORK:'off',GOFLAGS:'',GOTOOLCHAIN:'local',GOOS:'windows'}};
  const environment=JSON.parse(await command(['env','-json','GOMODCACHE'],options));
  if(typeof environment.GOMODCACHE!=='string' || !path.isAbsolute(environment.GOMODCACHE)) throw new Error('go env did not identify an absolute module cache');
  let existingCache;
  try { existingCache=await realpath(environment.GOMODCACHE); }
  catch(error) { if(error.code!=='ENOENT') throw error; }
  const temporaryParent=await realpath(tempRoot);
  // The absent cache's spelling is only a provisional exclusion check, not
  // evidence of physical identity. Only Go's official download creates it.
  const provisionalCache=existingCache??path.resolve(environment.GOMODCACHE);
  if(within(provisionalCache,temporaryParent)||within(repo,temporaryParent)) throw new Error('temporary module must be outside repository and GOMODCACHE');
  const temporary=await mkdtemp(path.join(temporaryParent,'native-sqlite-identity-'));
  const modfile=path.join(temporary,'experiment.mod'), sumfile=path.join(temporary,'experiment.sum');
  const replacement=path.join(temporary,'sqlite');
  let replacementOwned=false, failure;
  try {
    if(within(temporary,provisionalCache)) throw new Error('temporary directory overlaps proposed cache');
    await writeFile(modfile,mod); await writeFile(sumfile,sum);
    const downloaded=JSON.parse(await command(['mod','download','-modfile='+modfile,'-json',PIN.module+'@'+PIN.version],options));
    if(downloaded.Error || downloaded.Path!==PIN.module || downloaded.Version!==PIN.version || downloaded.Sum!==PIN.sum || downloaded.GoModSum!==PIN.goModSum || typeof downloaded.Dir!=='string') throw new Error('download identity/version/checksum mismatch');
    // Both cold and warm caches are re-resolved after download. No module copy
    // or experimental build occurs until all physical containment checks pass.
    const cache=await realpath(environment.GOMODCACHE);
    const actualTemporary=await realpath(temporary);
    const actualRepo=await realpath(repo);
    if(within(cache,actualTemporary)||within(actualTemporary,cache)||within(actualRepo,actualTemporary)||within(actualTemporary,actualRepo)) throw new Error('downloaded cache/repository overlaps temporary workspace; replacement must remain outside');
    if(existingCache && existingCache!==cache) throw new Error('module cache redirected during download');
    const source=await realpath(downloaded.Dir);
    if(source===cache || !within(cache,source)) throw new Error('download source is not within the actual Go module cache');
    const sourceMod=await readFile(path.join(source,'go.mod'),'utf8');
    if(!/^module modernc\.org\/sqlite\s*$/m.test(sourceMod)) throw new Error('download source module declaration mismatch');
    const selected=JSON.parse(await command(['list','-modfile='+modfile,'-m','-json',PIN.module],options));
    if(selected.Path!==PIN.module||selected.Version!==PIN.version||selected.Replace) throw new Error('resolved module selection differs from exact official version');
    await command(['mod','verify','-modfile='+modfile],options);
    await copyModule(source,replacement,()=>{replacementOwned=true;});
    // Re-verify the untouched cache after copying, before installing the patch.
    await command(['mod','verify','-modfile='+modfile],options);
    await writeFile(path.join(replacement,PATCH_NAME),patch,{flag:'wx'});
    // Slash-normalized quoted Go string works for spaces and Windows drive paths.
    await writeFile(modfile,mod+'\nreplace '+PIN.module+' => '+JSON.stringify(replacement.split(path.sep).join('/'))+'\n');
    const args=testArguments(modfile);
    log('Experimental command: go '+args.join(' '));
    const events=createEventValidator();
    await command(args,{...options,onStdout:chunk=>events.feed(chunk)});
    events.finish();
    log('Native event manifest verified: '+Object.values(EXPECTED_TESTS).reduce((total,names)=>total+names.length,0)+' required tests, 3 passes each, both package passes, no fail/skip events.');
  } catch(error) {
    failure=error;
  } finally {
    // Remove only runner-owned entries. In particular, a redirected cold cache
    // created unexpectedly inside this directory must never be recursively
    // deleted as though it were our module copy.
    const cleanupErrors=[];
    for(const owned of [modfile,sumfile,...(replacementOwned?[replacement]:[])]) {
      try { await rm(owned,{recursive:owned===replacement,force:true,maxRetries:0}); }
      catch(error) { cleanupErrors.push(error); }
    }
    try { await rmdir(temporary); } catch(error) { cleanupErrors.push(error); }
    if(cleanupErrors.length) failure=new AggregateError([...(failure?[failure]:[]),...cleanupErrors], 'experimental cleanup incomplete; unexpected entries retained; '+(failure?.message??'cleanup failure'));
  }
  if(failure) throw failure;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if(process.argv.length!==2) { console.error('This runner accepts no custom arguments.'); process.exitCode=1; }
  else runExperiment().catch(error=>{console.error(error.stack??String(error));process.exitCode=1;});
}
