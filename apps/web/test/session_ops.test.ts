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

test('real Go SDK and Chromium session branch, reversible compaction, clear and conversation rewind',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});
 await page.getByRole('button',{name:'＋ 新建会话'}).click();
 await send(page,'Never transmit secrets <script>alert(1)</script>');await page.locator('.badge').getByText('completed').waitFor();
 const source=(await client.listSessions())[0]!;const snap=await client.snapshot(source.id);const boundary=snap.runs[0]!.id;
 await send(page,'/branch latest');await page.locator('.provenance').getByText(/Parent/).waitFor();
 const child=(await client.listSessions())[0]!;assert.equal(child.provenance?.parent_session_id,source.id);assert.equal(child.provenance?.boundary_run_id,boundary);assert.equal(child.mode,source.mode);
 let view=await client.contextView(child.id);assert.equal(view.original[0]?.content,'Never transmit secrets <script>alert(1)</script>');assert.equal(view.selection,'original');
 await send(page,'/compact');await page.getByRole('status').getByText(/Compacted: local-omit-resolved-v1/).waitFor();
 view=await client.contextView(child.id);assert.equal(view.selection,'compacted');assert.equal(view.version?.omitted_messages,1);assert.ok(view.selected.some(m=>m.content.includes('Untrusted local compaction')));
 await send(page,'/compact view');await page.getByRole('status').getByText(/"selection": "compacted"/).waitFor();assert.equal(await page.locator('.notice script').count(),0);
 await send(page,'/compact original');await page.getByRole('status').getByText(/Original context selection restored/).waitFor();assert.equal((await client.contextView(child.id)).selection,'original');
 await page.getByRole('button',{name:'查看原会话（只读）'}).click();await page.locator('.badge').getByText('completed').waitFor();
 await send(page,'/rewind files');await page.getByRole('status').getByText(/not conversation rewind/).waitFor();assert.equal((await client.listSessions()).length,2);
 await send(page,'/rewind conversation latest');await page.locator('.provenance').getByText(/Parent/).waitFor();assert.equal((await client.listSessions())[0]!.provenance?.kind,'rewind');
 await send(page,'/clear');await page.getByRole('status').getByText(/clear confirm/).waitFor();assert.equal((await client.listSessions()).length,3);
 await send(page,'/clear confirm');await page.getByRole('status').getByText(/New empty conversation context/).waitFor();const empty=(await client.listSessions())[0]!;assert.equal((await client.contextView(empty.id)).original.length,0);
 const original=await client.snapshot(source.id);assert.equal(original.cursor,snap.cursor);assert.deepEqual(original.runs,snap.runs);
 assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'old\n');
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.locator('nav button').first().click();await page.locator('.provenance').getByText(/Parent/).waitFor();assert.equal((await client.listSessions()).length,4);
}));

test('delayed clear blocks parent submission, repeat operations and navigation; carries draft to confirmed child',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const parent=(await client.listSessions())[0]!;
 let release!:()=>void;const gate=new Promise<void>(r=>{release=r});let mark!:()=>void;const committed=new Promise<void>(r=>{mark=r});let writes=0;
 await page.route('**/api/v1/sessions/*/clear',async route=>{writes++;const response=await route.fetch();mark();await gate;await route.fulfill({response})});
 await send(page,'/clear confirm');await committed;
 await send(page,'new context only');await page.waitForTimeout(150);assert.equal((await client.snapshot(parent.id)).runs.length,0,'message reached old parent during clear');assert.equal(await page.getByLabel('消息').inputValue(),'new context only');
 assert.equal(await page.locator('nav button').first().isDisabled(),true);assert.equal(await page.getByRole('button',{name:'＋ 新建会话'}).isDisabled(),true);
 await send(page,'/clear confirm');assert.equal(writes,1);await page.getByLabel('消息').fill('preserved for child');
 release();await page.locator('.provenance').waitFor();await expect(page.getByLabel('消息')).toHaveValue('preserved for child');assert.equal((await client.listSessions()).length,2);assert.equal((await client.snapshot(parent.id)).runs.length,0);
}));
test('lost branch response survives reload with original identity and read-only recovery, never a second child',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await send(page,'original constraint');await page.locator('.badge').getByText('completed').waitFor();const parent=(await client.listSessions())[0]!;let requestID='';let writes=0;
 await page.route('**/api/v1/sessions/*/branch',async route=>{writes++;requestID=route.request().postDataJSON().request_id;await route.fetch();await route.abort('failed')});
 await send(page,'/branch latest');await page.locator('.context-operation').getByText(/unknown:/).waitFor();assert.ok(requestID);assert.match(await page.locator('.context-operation').innerText(),new RegExp(requestID));
 await send(page,'/branch latest');assert.equal(writes,1);assert.equal((await client.listSessions()).length,2);
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.locator('.context-operation').getByText(/unknown:/).waitFor();assert.match(await page.locator('.context-operation').innerText(),new RegExp(requestID));
 await page.getByRole('button',{name:'核对上下文操作（只读）'}).click();await page.locator('.provenance').waitFor();assert.equal(writes,1);assert.equal((await client.listSessions()).length,2);assert.equal((await client.snapshot(parent.id)).runs.length,1);assert.equal(await page.evaluate(()=>sessionStorage.getItem('agent-context-operation')),null);
}));
test('confirmed child is selected even if following session-list refresh fails',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();let failList=false;let writes=0;
 await page.route('**/api/v1/sessions',async route=>{if(failList&&route.request().method()==='GET')await route.abort('failed');else await route.continue()});
 await page.route('**/api/v1/sessions/*/clear',async route=>{writes++;const response=await route.fetch();failList=true;await route.fulfill({response})});
 await send(page,'/clear confirm');await page.locator('.provenance').waitFor();await page.getByRole('status').getByText(/Confirmed child.*session-list refresh failed/).waitFor();assert.equal(writes,1);assert.equal((await client.listSessions()).length,2);assert.equal(await page.locator('.context-operation').count(),0);
}));

test('unrecorded uncertain operation stays locked until explicit same-ID retry',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const requests:unknown[]=[];
 await page.route('**/api/v1/sessions/*/clear',async route=>{requests.push(route.request().postDataJSON());if(requests.length===1)await route.abort('failed');else await route.fulfill({response:await route.fetch()})});
 await send(page,'/clear confirm');await page.locator('.context-operation').getByText(/unknown:/).waitFor();assert.equal((await client.listSessions()).length,1);
 await page.getByRole('button',{name:'核对上下文操作（只读）'}).click();await page.getByRole('status').getByText(/operation_not_recorded/).waitFor();assert.equal(requests.length,1);assert.equal(await page.locator('nav button').first().isDisabled(),true);
 await send(page,'/retry-context confirm');await page.locator('.provenance').waitFor();assert.equal(requests.length,2);assert.deepEqual(requests[0],requests[1]);assert.equal((await client.listSessions()).length,2);
}));

test('compact and original recovery from another selected session selects source and unlocks without POST',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const source=(await client.listSessions())[0]!;
 await page.getByRole('button',{name:'＋ 新建会话'}).click();await expect(page.locator('nav button')).toHaveCount(2);const other=(await client.listSessions())[0]!;assert.notEqual(source.id,other.id);
 const posts:string[]=[];page.on('request',request=>{if(request.method()==='POST')posts.push(request.url())});
 for(const selection of ['compacted','original'] as const){
  const requestID='recover-'+selection;await client.compactSession(source.id,0,requestID,selection);
  await page.locator('nav button').filter({hasText:other.id.slice(0,8)}).click();
  await send(page,`/recover ${source.id} ${requestID}`);
  await expect(page.locator('nav button.active')).toContainText(source.id.slice(0,8));await expect(page.locator('.context-operation')).toHaveCount(0);await expect(page.getByRole('button',{name:'＋ 新建会话'})).toBeEnabled();
  await send(page,'/compact view');await page.getByRole('status').getByText(new RegExp(`"selection": "${selection}"`)).waitFor();assert.match(await page.getByRole('status').innerText(),new RegExp(source.id));assert.equal(posts.length,0);
 }
 // Already-mounted source also needs to acknowledge recovery without waiting
 // for a React mount that will not happen when the selected ID is unchanged.
 await send(page,`/recover ${source.id} recover-original`);await expect(page.locator('.context-operation')).toHaveCount(0);await expect(page.getByRole('button',{name:'＋ 新建会话'})).toBeEnabled();assert.equal(posts.length,0);
}));
