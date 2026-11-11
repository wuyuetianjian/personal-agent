// Windows-only, disconnected TEMP VFS experiment using the unmodified stock driver.
import { StringDecoder } from 'node:string_decoder';
import { spawn } from 'node:child_process';
import { open, mkdtemp, readFile, realpath, rm, rmdir } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const DEFAULT_REPO=fileURLToPath(new URL('../',import.meta.url));
export const PIN = Object.freeze({
  module: 'modernc.org/sqlite', version: 'v1.58.0',
  sum: 'h1:38u40/bwkfM7f0Myhosl+SEMltSDxnGdQf8o6Kjmys0=',
  goModSum: 'h1:rsD2CckafgObKC4DhBlGBf+RiHxkc3hINGt1Xw32tVY=',
});
export function validateInputs(mod, sum) {
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
}

export function testArguments(modfile) {
  return ['test', '-json', '-modfile='+modfile, '-tags=nativeprobe_sqlite_identity', '-count=3', '-timeout=10m', '-run=^('+Object.values(EXPECTED_TESTS).flat().join('|')+')$',
    './internal/nativefs/rootlease', './internal/nativefs/windowsprobe'];
}

export const EXPECTED_TESTS=Object.freeze({
  'agent/internal/nativefs/rootlease': Object.freeze(['TestNativeSQLiteTempSpillUsesOwnedScratch']),
  'agent/internal/nativefs/windowsprobe': Object.freeze(['TestNativeSQLiteTempVFSRejectsPathsAndFlags','TestNativeSQLiteTempVFSRegistrationOwnership',
    'TestNativeSQLiteTempScratchOffsetIO','TestNativeSQLiteTempScratchOwnership','TestNativeSQLiteTempScratchFailures','TestNativeSQLiteTempScratchParentMutation',
    'TestNativeSQLiteTempScratchConcurrentLifecycle','TestNativeSQLiteTempVFSStatementLifetime']),
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

export async function runExperiment({platform=process.platform,repo=DEFAULT_REPO,tempRoot=os.tmpdir(),command=executeGo,log=console.log,createFile=createOwnedFile}={}) {
  if(platform!=='win32') throw new Error('Windows-only stock SQLite TEMP experiment; refusing rather than skipping');
  repo=await realpath(repo);
  const [mod,sum]=await Promise.all(['go.mod','go.sum'].map(name=>readFile(path.join(repo,name),'utf8')));
  validateInputs(mod,sum);
  const options={cwd:repo,env:{...process.env,GOWORK:'off',GOFLAGS:'',GOTOOLCHAIN:'local',GOOS:'windows'}};
  const environment=JSON.parse(await command(['env','-json','GOMODCACHE','GOVERSION'],options));
  if(environment.GOVERSION!=='go1.26.4') throw new Error('stock experiment requires exactly Go go1.26.4');
  if(typeof environment.GOMODCACHE!=='string'||!path.isAbsolute(environment.GOMODCACHE)) throw new Error('absolute GOMODCACHE required');
  let existingCache;
  try { existingCache=await realpath(environment.GOMODCACHE); } catch(error) { if(error.code!=='ENOENT') throw error; }
  const temporaryParent=await realpath(tempRoot), provisionalCache=existingCache??path.resolve(environment.GOMODCACHE);
  if(within(repo,temporaryParent)||within(provisionalCache,temporaryParent)) throw new Error('temporary module must be outside repository/cache');
  const temporary=await mkdtemp(path.join(temporaryParent,'native-sqlite-temp-vfs-'));
  const modfile=path.join(temporary,'experiment.mod'),sumfile=path.join(temporary,'experiment.sum');
  let failure;
  const ownedFiles=[];
  try {
    if(within(temporary,provisionalCache)) throw new Error('temporary/cache overlap');
    await createFile(modfile,mod,()=>ownedFiles.push(modfile));
    await createFile(sumfile,sum,()=>ownedFiles.push(sumfile));
    log('Stock source '+PIN.module+' '+PIN.version+' sum='+PIN.sum+' goModSum='+PIN.goModSum+'; no patch, no replace; '+environment.GOVERSION);
    const downloaded=JSON.parse(await command(['mod','download','-modfile='+modfile,'-json',PIN.module+'@'+PIN.version],options));
    if(downloaded.Error||downloaded.Path!==PIN.module||downloaded.Version!==PIN.version||downloaded.Sum!==PIN.sum||downloaded.GoModSum!==PIN.goModSum||typeof downloaded.Dir!=='string') throw new Error('download source identity/version/checksum mismatch');
    const cache=await realpath(environment.GOMODCACHE),actualTemporary=await realpath(temporary),actualRepo=await realpath(repo),source=await realpath(downloaded.Dir);
    if(within(cache,actualTemporary)||within(actualTemporary,cache)||within(actualRepo,actualTemporary)||within(actualTemporary,actualRepo)) throw new Error('downloaded cache/repository overlaps temporary workspace');
    if(existingCache&&existingCache!==cache)throw new Error('module cache redirected during download');
    if(source===cache||!within(cache,source))throw new Error('download source outside cache');
    if(!/^module modernc\.org\/sqlite\s*$/m.test(await readFile(path.join(source,'go.mod'),'utf8')))throw new Error('source module declaration mismatch');
    const selected=JSON.parse(await command(['list','-modfile='+modfile,'-m','-json',PIN.module],options));
    if(selected.Path!==PIN.module||selected.Version!==PIN.version||selected.Replace||typeof selected.Dir!=='string'||await realpath(selected.Dir)!==source)throw new Error('selected module differs from verified stock source');
    await command(['mod','verify','-modfile='+modfile],options);
    let testFailure;
    try {
      const args=testArguments(modfile),events=createEventValidator();
      log('Stock TEMP experimental command: go '+args.join(' '));
      await command(args,{...options,onStdout:chunk=>events.feed(chunk)});
      events.finish();
    } catch(error) { testFailure=error; }
    // Re-verify even after a test failure, without replacing that original error.
    try { await command(['mod','verify','-modfile='+modfile],options); }
    catch(error) { testFailure=testFailure?new AggregateError([testFailure,error],'native test and post verification failed'):error; }
    if(testFailure)throw testFailure;
    log('Stock TEMP manifest verified: '+Object.values(EXPECTED_TESTS).flat().length+' required tests, run/pass three times each, both package passes, no failure/skip. Experimental only, not production qualification.');
  } catch(error) { failure=error; }
  finally {
    const errors=[];
    for(const owned of ownedFiles)try{await rm(owned,{force:true});}catch(error){errors.push(error);}
    try{await rmdir(temporary);}catch(error){errors.push(error);}
    if(errors.length)failure=new AggregateError([...(failure?[failure]:[]),...errors],'stock experiment cleanup incomplete; unexpected entries retained');
  }
  if(failure)throw failure;
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  if(process.argv.length!==2){console.error('This runner accepts no custom arguments.');process.exitCode=1;}
  else runExperiment().catch(error=>{console.error(error.stack??String(error));process.exitCode=1;});
}

export async function createOwnedFile(file,content,onCreated) {
  const handle=await open(file,'wx');
  try {
    onCreated(); // Only successful exclusive creation establishes cleanup ownership.
    await handle.writeFile(content);
  } finally { await handle.close(); }
}
