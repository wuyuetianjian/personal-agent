import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {existsSync,rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {buildHookCLI,createHookCLIDirectory} from './build_hooks_cli.mjs';

test('compiled top-level help exposes Hook prepare, separate decisions and original-request GET recovery without authentication',()=>{
 const dir=createHookCLIDirectory();
 try{
  assert.equal(existsSync(join(dir,'node_modules')),false,'compiled fixture must resolve the owning package dependencies without a link');
  const entry=join(dir,'cli.mjs');
  buildHookCLI(entry);
  const env={...process.env};
  delete env.PA_AGENT_TOKEN;
  const result=spawnSync(process.execPath,[entry,'--help'],{cwd:tmpdir(),env,encoding:'utf8',timeout:15000});
  assert.equal(result.error,undefined);
  assert.equal(result.status,0,result.stderr);
  assert.equal(result.stdout,'');
  const help=result.stderr;
  assert.match(help,/--hooks list\|revisions\|show\|import\|enable\|disable\|confirm\|cancel\|lookup\|receipts --session ID/);
  assert.match(help,/Hooks:.*--policy-file PATH.*--revision ID.*--run-id ID.*--cursor CURSOR.*--format text\|json/);
  assert.match(help,/Hooks prepare:.*import.*enable.*disable.*--request-id PREVIEW.*only prepare/);
  assert.match(help,/Hooks decision:.*confirm\|cancel.*--operation-id PREVIEW.*--digest SHA256.*--request-id DISTINCT_DECISION/);
  assert.match(help,/Run confirmation is separate/);
  assert.match(help,/Hooks recovery:.*lookup --request-id ORIGINAL_REQUEST.*GET only.*original prepare\/decision ID.*never retry POST automatically/);
 }finally{
  rmSync(dir,{recursive:true,force:true});
 }
});


test('standalone Hook builder accepts an output path with spaces and produces the real runnable CLI',()=>{
 const dir=createHookCLIDirectory();
 try{
  const entry=join(dir,'cli with spaces.mjs');
  const build=spawnSync(process.execPath,[fileURLToPath(new URL('./build_hooks_cli.mjs',import.meta.url)),entry],{encoding:'utf8',timeout:15000});
  assert.equal(build.error,undefined);
  assert.equal(build.status,0,build.stderr);
  assert.equal(existsSync(entry),true,'standalone builder must invoke buildHookCLI');
  const result=spawnSync(process.execPath,[entry,'--help'],{cwd:tmpdir(),encoding:'utf8',timeout:15000});
  assert.equal(result.error,undefined);
  assert.equal(result.status,0,result.stderr);
  assert.match(result.stderr,/--hooks/);
 }finally{rmSync(dir,{recursive:true,force:true})}
});
