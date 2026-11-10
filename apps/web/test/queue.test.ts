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


test('real queue FIFO: draft by default, explicit admission, withdrawal distinct from active cancel, no reload replay',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await send(page,'wait');await page.locator('.badge').getByText('model',{exact:true}).waitFor();const source=(await client.listSessions())[0]!;
 await send(page,'retained draft');await expect(page.getByLabel('消息')).toHaveValue('retained draft');assert.equal((await client.queue(source.id)).items.length,0);assert.equal((await client.snapshot(source.id)).runs.length,1);
 const first='first <script>window.unsafe=true</script>  ';await page.getByLabel('消息').fill(first);await page.getByRole('button',{name:'加入队列',exact:true}).click();await expect(page.getByLabel('消息')).toHaveValue('');await page.locator('.queue-item').first().getByText(/pending/).waitFor();
 await send(page,'/queue add withdraw this');await expect(page.locator('.queue-item')).toHaveCount(2);const doomed=(await client.queue(source.id)).items[1]!;
 await page.getByRole('button',{name:'撤回 '+doomed.id.slice(0,8),exact:true}).click();await page.locator('.queue-item').nth(1).getByText(/withdrawn/).waitFor();
 await send(page,'/queue add last  exact  ');await expect(page.locator('.queue-item')).toHaveCount(3);assert.equal((await client.queue(source.id)).items[2]!.content,'last  exact  ');assert.equal(await page.locator('.input-queue script').count(),0);assert.ok((await page.locator('.input-queue').boundingBox())!.height<=240,'queue inspection must remain bounded on narrow screens');
 await page.getByRole('button',{name:'停止',exact:true}).click();await expect.poll(async()=>{const q=await client.queue(source.id);return q.items[2]?.phase},{timeout:15000}).toBe('completed');
 const snap=await client.snapshot(source.id);assert.equal(snap.runs.length,3);assert.deepEqual(snap.runs.map(r=>r.input),['wait',first,'last  exact  ']);assert.equal(snap.runs[0]!.state,'cancelled');const q=await client.queue(source.id);assert.equal(q.items[0]!.run_id,snap.runs[1]!.id);assert.equal(q.items[1]!.state,'withdrawn');
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.locator('nav button').first().click();await page.getByLabel('消息').waitFor();assert.equal((await client.snapshot(source.id)).runs.length,3);assert.equal(await readFile(join(info.workspace,'note.txt'),'utf8'),'old\n');
}));
test('lost queue acknowledgement quarantines original ID across reload; explicit same-ID retry and withdrawal recovery',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await send(page,'wait');await page.locator('.badge').getByText('model',{exact:true}).waitFor();const source=(await client.listSessions())[0]!;let writes=0;let requestID='';
 await page.route('**/api/v1/sessions/*/queue',async route=>{if(route.request().method()==='GET'){await route.continue();return}writes++;requestID=route.request().postDataJSON().request_id;await route.fetch();await route.abort('failed')});
 await page.getByLabel('消息').fill('once only');await page.getByRole('button',{name:'加入队列',exact:true}).click();await page.locator('.queue-operation').getByText(/unknown:/).waitFor();assert.equal(await page.getByRole('button',{name:'加入队列',exact:true}).isDisabled(),true);assert.equal(await page.locator('nav button').first().isDisabled(),true);assert.equal((await client.queue(source.id)).items.length,1);assert.match(await page.locator('.queue-operation').innerText(),new RegExp(requestID));
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.locator('.queue-operation').getByText(/unknown:/).waitFor();await page.getByRole('button',{name:'核对队列操作（只读）'}).click();await expect(page.locator('.queue-operation')).toHaveCount(0);assert.equal(writes,1);assert.equal(await page.evaluate(()=>sessionStorage.getItem('agent-queue-operation')),null);
 await page.unroute('**/api/v1/sessions/*/queue');const bodies:unknown[]=[];
 await page.route('**/api/v1/sessions/*/queue',async route=>{if(route.request().method()==='GET'){await route.continue();return}bodies.push(route.request().postDataJSON());if(bodies.length===1)await route.abort('failed');else await route.fulfill({response:await route.fetch()})});
 await send(page,'/queue add retry exact');await page.locator('.queue-operation').getByText(/unknown:/).waitFor();await send(page,'/queue recover');await page.getByRole('status').getByText(/queue_operation_not_found/).waitFor();assert.equal(bodies.length,1);await send(page,'/queue retry confirm');await expect(page.locator('.queue-operation')).toHaveCount(0);assert.deepEqual(bodies[0],bodies[1]);assert.equal((await client.queue(source.id)).items.length,2);
 let withdrawals=0;await page.route('**/queue/*/withdraw',async route=>{withdrawals++;await route.fetch();await route.abort('failed')});const item=(await client.queue(source.id)).items[0]!;await page.getByRole('button',{name:'撤回 '+item.id.slice(0,8),exact:true}).click();await page.locator('.queue-operation').getByText(/unknown:/).waitFor();await page.getByRole('button',{name:'核对队列操作（只读）'}).click();await expect(page.locator('.queue-operation')).toHaveCount(0);assert.equal(withdrawals,1);assert.equal((await client.queue(source.id)).items[0]!.state,'withdrawn');
 await client.cancelRun((await client.snapshot(source.id)).runs[0]!.id,crypto.randomUUID());
}));
