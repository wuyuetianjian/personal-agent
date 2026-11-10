import {AgentClient} from '@personal-agent/client';
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,readFile,realpath,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {chromium,expect} from '@playwright/test';
import type {Page} from '@playwright/test';

async function fixture(args:string[],work:(page:Page,info:{url:string;workspace:string},token:string)=>Promise<void>){
 const dir=await mkdtemp(join(tmpdir(),'agent-acceptance-'));const bin=join(dir,'fixture');
 const build=spawnSync('go',['build','-tags','webui','-o',bin,'./cmd/agent-fixture'],{encoding:'utf8',timeout:60000});assert.equal(build.status,0,build.stderr);
 const token='acceptance-local';if(args.includes('--safe-exec')){for(const flag of ['--workspace','--runtime-parent']){const refused=spawnSync(bin,['--safe-exec',flag,dir],{env:{...process.env,PA_AGENT_TOKEN:token},encoding:'utf8',timeout:5000});assert.equal(refused.status,1);assert.match(refused.stderr,/requires owned temporary workspace/)}await assert.rejects(readFile(join(dir,'note.txt')),{code:'ENOENT'})}const srv=spawn(bin,args,{env:{...process.env,PA_AGENT_TOKEN:token},stdio:['ignore','pipe','pipe']});let browser;let stderr='';srv.stderr.on('data',b=>stderr+=b);
 try{
  const info=await new Promise<{url:string;workspace:string}>((resolve,reject)=>{let line='';const timer=setTimeout(()=>reject(new Error('fixture startup timeout '+stderr)),10000);srv.once('exit',code=>{clearTimeout(timer);reject(new Error('fixture exit '+code+' '+stderr))});srv.stdout.on('data',b=>{line+=b;if(line.includes('\n')){clearTimeout(timer);resolve(JSON.parse(line.split('\n')[0]!))}})});
  browser=await chromium.launch({executablePath:process.env['CHROMIUM_PATH']??'/usr/bin/chromium',args:['--no-sandbox']});const page=await browser.newPage({viewport:{width:390,height:844}});page.setDefaultTimeout(10000);
  // Abort the real fetch transport on demand, leaving SDK retries/replay untouched.
  await page.addInitScript(()=>{const original=window.fetch.bind(window);let stream:AbortController|undefined;(window as unknown as {dropStream:()=>void}).dropStream=()=>stream?.abort();window.fetch=(input,init)=>{if(String(input).includes('/events?')){stream=new AbortController();const signals=[stream.signal];if(init?.signal)signals.push(init.signal);return original(input,{...init,signal:AbortSignal.any(signals)})}return original(input,init)}});
  await page.goto(info.url);await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.getByLabel('新会话权限').waitFor();await work(page,info,token);
 }finally{await browser?.close();if(srv.exitCode===null){await new Promise<void>(resolve=>{const timer=setTimeout(()=>srv.kill('SIGKILL'),5000);srv.once('exit',()=>{clearTimeout(timer);resolve()});srv.kill('SIGINT')})}await rm(dir,{recursive:true,force:true})}
}
async function send(page:Page,value:string){await page.getByLabel('消息').fill(value);await page.getByLabel('消息').press('Enter')}

test('native file undo uses real SDK, fresh exact approval, one effect and read-only lost-ack recovery',{timeout:90000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});let writes=0,decisions=0,messages=0;
 page.on('request',r=>{if(r.method()==='POST'&&r.url().endsWith('/file-undo'))writes++;if(r.method()==='POST'&&r.url().endsWith('/decision'))decisions++;if(r.method()==='POST'&&r.url().endsWith('/messages'))messages++});
 await page.getByLabel('新会话权限').selectOption('acceptEdits');await page.getByRole('button',{name:'＋ 新建会话'}).click();await send(page,'edit');await page.locator('.badge').getByText('completed').waitFor();assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'new 你好\n');assert.equal(decisions,0);
 const session=(await client.listSessions())[0]!;const inventory=await client.editCheckpoints(session.id);assert.equal(inventory.checkpoints.length,1);const checkpoint=inventory.checkpoints[0]!;assert.equal(checkpoint.review.old_content,'old\n');assert.equal(checkpoint.review.new_content,'new 你好\n');
 await send(page,'/rewind files');await page.getByRole('status').getByText(/"checkpoints"/).waitFor();assert.equal(writes,0);assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'new 你好\n');
 let requestID='';await page.route('**/file-undo',async route=>{requestID=route.request().postDataJSON().request_id;await route.fetch();await route.abort('failed')},{times:1});
 await send(page,'/rewind files '+checkpoint.id);await page.locator('.context-operation').getByText(/unknown:/).waitFor();assert.equal(writes,1);assert.ok(requestID);assert.equal(await page.locator('nav button').first().isDisabled(),true);
 await send(page,'/rewind files '+checkpoint.id);assert.equal(writes,1);assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'new 你好\n');
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.locator('.context-operation').getByText(/unknown:/).waitFor();await page.getByRole('button',{name:'核对上下文操作（只读）'}).click();
 const review=page.locator('.approval');const approve=review.getByRole('button',{name:'批准此次操作'});await expect(approve).toBeEnabled();assert.equal(writes,1);assert.equal(messages,1);const shown=await review.innerText();for(const text of [checkpoint.id,checkpoint.source_run_id,info.workspace,checkpoint.root_identity,'new 你好','old','Fresh approval always required'])assert.ok(shown.includes(text),text+' missing');
 const snap=await client.snapshot(session.id);const undo=snap.runs.at(-1)!;assert.notEqual(undo.approval!.id,checkpoint.id);assert.equal(undo.text,'');assert.equal(undo.input,'Explicit native file undo: '+checkpoint.id);
 await approve.dblclick({delay:5});await page.locator('.badge').getByText('completed').waitFor();assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'old\n');assert.equal(decisions,1);
 let after=await client.editCheckpoints(session.id);assert.equal(after.checkpoints.length,1);assert.equal(after.checkpoints[0]!.disposition,'undone');assert.equal(after.checkpoints[0]!.undo_run_id,undo.id);assert.equal((await client.snapshot(session.id)).runs.length,2);
 await assert.rejects(client.decideApproval(undo.approval!.id,undo.approval!.digest,'approved','duplicate-decision'));assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'old\n');
 await send(page,'/rewind files '+checkpoint.id);await page.getByRole('status').getByText(/context_operation_rejected/).waitFor();assert.equal((await client.snapshot(session.id)).runs.length,2);assert.equal(await page.locator('.context-operation').count(),0);assert.equal(await page.locator('.approval').count(),0);
}));
