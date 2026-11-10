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

test('real Chromium MCP explicit launch, exact approval, deny and one effect',{timeout:70000},async()=>fixture(['--safe-mcp'],async(page,info)=>{
 await assert.rejects(readFile(join(info.workspace,'mcp-marker.txt.launch')),{code:'ENOENT'});
 await page.getByLabel('新会话权限').selectOption('acceptEdits');
 await page.getByLabel('启动已配置 MCP（非沙箱）').check();
 await page.getByRole('button',{name:'＋ 新建会话'}).click();
 await page.getByText(/discovered/).first().waitFor();
 assert.equal((await readFile(join(info.workspace,'mcp-marker.txt.launch'),'utf8')).trim(),'launch');
 await send(page,'/mcp');await page.getByRole('status').getByText(/HTTP, Tasks/).waitFor();
 await assert.rejects(readFile(join(info.workspace,'mcp-marker.txt')),{code:'ENOENT'});
 await send(page,'mcp');const review=page.locator('.approval');await review.getByRole('button',{name:'批准此次操作'}).waitFor();
 const exact=JSON.parse(await review.locator('pre').innerText());assert.equal(exact.server_id,'owned-fixture');assert.equal(exact.tool,'write');assert.equal(exact.arguments,'{"value":"hello 世界"}');assert.match(exact.schema_digest,/^[a-f0-9]{64}$/);assert.match(exact.config_fingerprint,/^[a-f0-9]{64}$/);assert.match(exact.warning,/NON-SANDBOXED.*current OS user/);assert.match(exact.environment_policy,/no inherited/);assert.equal(exact.unavailable_reason,'');assert.ok((await page.locator('.timeline').boundingBox())!.height>=180,'MCP status notice collapsed the approval timeline');assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'MCP review caused horizontal overflow');await review.getByRole('button',{name:'批准此次操作'}).scrollIntoViewIfNeeded();await page.screenshot({path:'/tmp/task7b-mcp-web.png',fullPage:true});
 await review.getByRole('button',{name:'拒绝',exact:true}).click();await page.locator('.badge').getByText('failed').waitFor();await assert.rejects(readFile(join(info.workspace,'mcp-marker.txt')),{code:'ENOENT'});
 await send(page,'mcp');await review.getByRole('button',{name:'批准此次操作'}).waitFor();await review.getByRole('button',{name:'批准此次操作'}).dblclick();await page.locator('.badge').getByText('completed').waitFor();
 const markers=(await readFile(join(info.workspace,'mcp-marker.txt'),'utf8')).trim().split('\n');assert.equal(markers.length,1);assert.equal(JSON.parse(markers[0]!).arguments.value,'hello 世界');
 const result=page.locator('.tool').last();await result.locator('summary').click();assert.match(await result.innerText(),/known_response/);assert.match(await result.innerText(),/owned result/);assert.equal(await result.locator('a').count(),0);
}));
