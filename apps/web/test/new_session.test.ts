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

test('active chooser labels new sessions only and lost admission recovers after reload without POST',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});
 await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();
 assert.equal((await client.listSessions()).length,0,'chooser created a hidden session');
 await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const source=(await client.listSessions())[0]!;
 const preview=await client.prepareOperator(source.id,'project.create','','Exact <script> 名称','catalog-create');await client.decideOperator(source.id,preview.operation_id,preview.digest,'confirm','catalog-confirm');
 await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await page.getByLabel('Next-session label / 下个会话标签').selectOption(preview.target_id);
 await page.getByLabel('消息').fill('draft remains private');let writes=0,request='';
 await page.route('**/api/v1/sessions',async route=>{if(route.request().method()!=='POST'){await route.continue();return};writes++;request=route.request().postDataJSON().request_id;await route.fetch();await route.abort('failed')});
 await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.locator('.admission-operation').getByText(/unknown:/).waitFor();assert.equal(await page.getByRole('button',{name:'＋ 新建会话'}).isDisabled(),true);assert.equal(await page.getByLabel('消息').inputValue(),'draft remains private');await page.getByLabel('消息').press('Enter');assert.equal((await client.snapshot(source.id)).runs.length,0);assert.equal(writes,1);
 const saved=await page.evaluate(()=>sessionStorage.getItem('agent-session-admission'));assert.ok(saved?.includes(request));assert.ok(!saved?.includes('draft')&&!saved?.includes(preview.target_id));
 await page.reload();await page.getByLabel('服务令牌').fill(token);await page.getByRole('button',{name:'连接',exact:true}).click();await page.getByRole('button',{name:'Recover admission / 只读核对新会话'}).click();await page.locator('.current-project-label').getByText(/Exact <script> 名称/).waitFor();assert.equal(writes,1);assert.equal((await client.listSessions()).length,2);assert.equal(await page.locator('script:not([src])').count(),0);
 const child=(await client.listSessions())[0]!;assert.equal(child.project_label?.project_id,preview.target_id);assert.equal((await client.snapshot(child.id)).runs.length,0);
 const rename=await client.prepareOperator(source.id,'project.rename',preview.target_id,'renamed','rename');await client.decideOperator(source.id,rename.operation_id,rename.digest,'confirm','rename-yes');
 const archive=await client.prepareOperator(source.id,'project.archive',preview.target_id,'archived','archive');await client.decideOperator(source.id,archive.operation_id,archive.digest,'confirm','archive-yes');
 await send(page,'/clear confirm');await page.locator('.provenance').waitFor();await page.locator('.current-project-label').getByText(/Exact <script> 名称/).waitFor();await page.getByLabel('Next-session label / 下个会话标签').selectOption('');await page.unroute('**/api/v1/sessions');await page.getByRole('button',{name:'＋ 新建会话'}).click();await expect(page.locator('.current-project-label')).toContainText('No project label');assert.equal((await client.listSessions())[0]!.project_label,undefined);
}));

test('delayed labeled MCP admission quarantines clicks, navigation and draft; known result survives storage cleanup/list failure',{timeout:70000},async()=>fixture(['--safe-mcp'],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const parent=(await client.listSessions())[0]!;
 const p=await client.prepareOperator(parent.id,'project.create','','MCP label','create-label');await client.decideOperator(parent.id,p.operation_id,p.digest,'confirm','confirm-label');
 await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await page.getByLabel('Next-session label / 下个会话标签').selectOption(p.target_id);await page.getByLabel('新会话权限').selectOption('default');await page.getByLabel('启动已配置 MCP（非沙箱）').check();await page.getByLabel('消息').fill('private carried draft');
 await page.evaluate(()=>{const remove=Storage.prototype.removeItem;Storage.prototype.removeItem=function(key){if(key==='agent-session-admission')throw Error('owned storage cleanup failure');return remove.call(this,key)}});
 let release!:()=>void,committed!:()=>void;const gate=new Promise<void>(r=>release=r),done=new Promise<void>(r=>committed=r);let writes=0,failList=false;
 await page.route('**/api/v1/sessions',async route=>{if(route.request().method()==='POST'){writes++;const response=await route.fetch();committed();await gate;failList=true;await route.fulfill({response})}else if(failList)await route.abort('failed');else await route.continue()});
 await page.getByRole('button',{name:'＋ 新建会话'}).click();await done;await page.locator('.admission-operation').getByText(/sending:/).waitFor();assert.equal(await page.getByLabel('Next-session label / 下个会话标签').isDisabled(),true);assert.equal(await page.locator('nav button').first().isDisabled(),true);
 await page.getByRole('button',{name:'＋ 新建会话'}).evaluate((el:HTMLButtonElement)=>{el.click();el.click()});await page.getByLabel('消息').press('Enter');await page.getByLabel('消息').press('Enter');await page.getByRole('button',{name:'Cancel intent / 请求取消意图'}).click();await page.locator('.admission-operation').getByText(/NOT proof/).waitFor();assert.equal(writes,1);assert.equal((await client.snapshot(parent.id)).runs.length,0);
 await page.evaluate(()=>{location.hash='manage/projects'});await page.waitForTimeout(100);assert.equal(await page.locator('.operator-panel').count(),0);
 release();await page.locator('.current-project-label').getByText(/MCP label/).waitFor();await expect(page.getByLabel('消息')).toHaveValue('private carried draft');await page.getByRole('status').getByText(/retained; session-list refresh failed/).waitFor();assert.equal(await page.locator('.admission-operation').count(),0);assert.equal(writes,1);assert.equal((await readFile(join(info.workspace,'mcp-marker.txt.launch'),'utf8')).trim(),'launch');assert.equal((await client.listSessions()).length,2);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'/tmp/task7e6b-label-390.png',fullPage:true});
}));

test('archived selection refuses before claim and retains original session; catalog failure does not remove none',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const source=(await client.listSessions())[0]!;const p=await client.prepareOperator(source.id,'project.create','','Race label','race-create');await client.decideOperator(source.id,p.operation_id,p.digest,'confirm','race-confirm');await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await page.getByLabel('Next-session label / 下个会话标签').selectOption(p.target_id);const a=await client.prepareOperator(source.id,'project.archive',p.target_id,'archived','archive');await client.decideOperator(source.id,a.operation_id,a.digest,'confirm','archived');
 await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByRole('status').getByText(/project_label_unavailable/).waitFor();assert.equal(await page.locator('.admission-operation').count(),0);assert.equal((await client.listSessions()).length,1);await page.route('**/api/v1/catalog/projects?*',r=>r.abort('failed'));await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await page.getByText(/Catalog unavailable/).waitFor();await page.getByLabel('Next-session label / 下个会话标签').selectOption('');await page.getByRole('button',{name:'＋ 新建会话'}).click();await expect(page.locator('aside nav button')).toHaveCount(2);assert.equal((await client.listSessions())[0]!.project_label,undefined);
}));

test('active chooser advances bounded cursor pages without hidden session or automatic choice',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();const source=(await client.listSessions())[0]!;let finalID='';
 for(let n=0;n<26;n++){const p=await client.prepareOperator(source.id,'project.create','','label-'+n,'page-create-'+n);await client.decideOperator(source.id,p.operation_id,p.digest,'confirm','page-confirm-'+n);finalID=p.target_id}
 const picker=page.getByLabel('Next-session label / 下个会话标签');await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await expect(picker.locator('option')).toHaveCount(26);await expect(picker).toHaveValue('');assert.equal((await client.listSessions()).length,1);await page.getByRole('button',{name:'More labels / 下一页标签'}).click();await expect(picker.locator('option')).toHaveCount(2);await expect(picker).toHaveValue('');await picker.selectOption(finalID);await page.getByRole('button',{name:'Load active labels / 读取标签'}).click();await expect(picker).toHaveValue(finalID);await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.locator('.current-project-label').getByText(/label-25/).waitFor();assert.equal((await client.listSessions())[0]!.project_label?.project_id,finalID);assert.equal((await client.listSessions()).length,2);
}));

test('protocol preflight refusal leaves draft editable and permits only a new explicit admission',{timeout:70000},async()=>fixture([],async(page,info,token)=>{
 const client=new AgentClient({baseURL:info.url,token:()=>token});await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();await page.getByLabel('消息').fill('keep preflight draft');let posts=0,reads=0;
 await page.route('**/api/v1/sessions',async route=>{if(route.request().method()==='POST')posts++;await route.continue()});
 for(const failure of ['network','old404','malformed']){
  await page.route('**/api/v1/protocol',async route=>{reads++;if(failure==='network')await route.abort('failed');else await route.fulfill({status:failure==='old404'?404:200,contentType:'application/json',body:failure==='malformed'?'{':'{}'})});
  await page.getByRole('button',{name:'＋ 新建会话'}).click();await expect(page.getByRole('status')).toContainText('session admission not sent');await expect(page.getByRole('button',{name:'＋ 新建会话'})).toBeEnabled();assert.equal(posts,0);assert.equal(await page.locator('.admission-operation').count(),0);await expect(page.getByLabel('消息')).toHaveValue('keep preflight draft');assert.equal(await page.evaluate(()=>sessionStorage.getItem('agent-session-admission')),null);await page.unroute('**/api/v1/protocol');
 }
 assert.equal(reads,3);assert.equal((await client.listSessions()).length,1);await page.getByRole('button',{name:'＋ 新建会话'}).click();await expect(page.locator('aside nav button')).toHaveCount(2);assert.equal(posts,1);await expect(page.getByLabel('消息')).toHaveValue('keep preflight draft');assert.equal((await client.listSessions()).length,2);
}));

test('explicit unavailable workspace modes preserve choice and block repeated Web admission before POST',{timeout:70000},async()=>fixture([],async(page)=>{
 let writes=0;
 await page.route('**/api/v1/sessions',async route=>{if(route.request().method()==='POST')writes++;await route.continue()});
 await page.route('**/api/v1/status',async route=>{const response=await route.fetch();const body=await response.json();body.workspace={...body.workspace,Files:{Enabled:false,Reason:'native fixture reason <not HTML>'}};await route.fulfill({response,json:body})});
 await page.reload();await page.getByLabel('服务令牌').fill('acceptance-local');await page.getByRole('button',{name:'连接',exact:true}).click();
 const mode=page.getByLabel('新会话权限');
 for(const value of ['default','plan','acceptEdits']){
  await mode.selectOption(value);
  await expect(mode.locator(`option[value="${value}"]`)).toContainText('不可用');
  await page.getByRole('button',{name:'＋ 新建会话'}).evaluate((el:HTMLButtonElement)=>{el.click();el.click()});
  await expect(page.getByRole('status')).toContainText('native fixture reason <not HTML>');
  await expect(mode).toHaveValue(value);assert.equal(writes,0);assert.equal(await page.locator('.admission-operation').count(),0);
  assert.equal(await page.evaluate(()=>sessionStorage.getItem('agent-session-admission')),null);
 }
 await mode.selectOption('chat_only');await page.getByRole('button',{name:'＋ 新建会话'}).click();await page.getByLabel('消息').waitFor();assert.equal(writes,1);
}));

test('unknown or stale enabled workspace status leaves server refusal and read-only recovery authoritative',{timeout:70000},async()=>fixture([],async(page)=>{
 let files:unknown=undefined,writes=0,request='',lookups=0;
 await page.route('**/api/v1/status',async route=>{const response=await route.fetch();const body=await response.json();body.workspace={Files:files};await route.fulfill({response,json:body})});
 // A transport fixture for the real Web/client admission state machine, not
 // evidence of backend effects. Actual 409 native refusal has its own Go CI.
 await page.route('**/api/v1/sessions',async route=>{if(route.request().method()!=='POST'){await route.continue();return}writes++;request=route.request().postDataJSON().request_id;await route.fulfill({status:409,json:{error:{code:'workspace_unavailable_choose_chat_only'}}})});
 await page.route('**/api/v1/admissions/*',async route=>{lookups++;assert.ok(route.request().url().endsWith(request));assert.equal(route.request().method(),'GET');await route.fulfill({json:{request_id:request,session_id:'unpublished',state:'unresolved',mcp_opt_in:false,reason:'fixture refusal settled'}})});
 for(const value of [undefined,{Enabled:'false'},{Enabled:true}]){
  files=value;await page.reload();await page.getByLabel('服务令牌').fill('acceptance-local');await page.getByRole('button',{name:'连接',exact:true}).click();
  const mode=page.getByLabel('新会话权限');await mode.selectOption('plan');await expect(mode.locator('option[value="plan"]')).toHaveText('计划');const before=writes;
  await page.getByRole('button',{name:'＋ 新建会话'}).evaluate((el:HTMLButtonElement)=>{el.click();el.click()});
  await expect(page.locator('.admission-operation')).toContainText('unknown:');assert.equal(writes,before+1);await expect(mode).toHaveValue('plan');await expect(mode).toBeDisabled();
  await expect(page.getByRole('status')).toContainText('workspace_unavailable_choose_chat_only');
  await page.getByRole('button',{name:'Recover admission / 只读核对新会话'}).click();await expect(page.locator('.admission-operation')).toContainText('unresolved:');assert.equal(writes,before+1);
  await page.getByRole('button',{name:'Acknowledge failed claim; allow a new explicit action / 确认失败记录并解锁'}).click();await expect(mode).toBeEnabled();await expect(mode).toHaveValue('plan');
 }
 assert.equal(writes,3);assert.equal(lookups,3);
}));
