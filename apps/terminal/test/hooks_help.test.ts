import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtempSync,rmSync,symlinkSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {buildHookCLI} from './build_hooks_cli.mjs';

test('compiled top-level help exposes Hook prepare, separate decisions and original-request GET recovery without authentication',()=>{
 const dir=mkdtempSync(join(tmpdir(),'hooks-help-'));
 try{
  symlinkSync(resolve('apps/terminal/node_modules'),join(dir,'node_modules'),process.platform==='win32'?'junction':'dir');
  const entry=join(dir,'cli.mjs');
  buildHookCLI(entry);
  const env={...process.env};
  delete env.PA_AGENT_TOKEN;
  const result=spawnSync(process.execPath,[entry,'--help'],{cwd:dir,env,encoding:'utf8',timeout:15000});
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
