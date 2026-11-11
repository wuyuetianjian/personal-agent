import type {ChildProcess} from 'node:child_process';

export const fixtureName=(platform:NodeJS.Platform=process.platform)=>platform==='win32'?'fixture.exe':'fixture';

// Install immediately after spawn, including the error handler for ENOENT.
export function readFixtureInfo<T={url:string}>(child:ChildProcess,timeout=10000):Promise<T>{
 return new Promise((resolve,reject)=>{
  let stdout='',stderr='';
  const cleanup=()=>{clearTimeout(timer);child.off('error',error);child.off('close',closed);child.stdout?.off('data',data);child.stderr?.off('data',diagnostic)};
  const error=(err:Error)=>{cleanup();reject(err)};
  const closed=()=>error(Error(`fixture exited before startup (${child.exitCode ?? child.signalCode}): ${stderr}`));
  const diagnostic=(chunk:Buffer)=>{stderr=(stderr+String(chunk)).slice(-16384)};
  const data=(chunk:Buffer)=>{stdout+=String(chunk);if(stdout.includes('\n')){try{const info=JSON.parse(stdout.split('\n')[0]!);cleanup();resolve(info)}catch(err){error(Error(`invalid fixture startup: ${String(err)}; ${stderr}`))}}};
  const timer=setTimeout(()=>error(Error(`fixture startup timeout: ${stderr}`)),timeout);
  child.once('error',error);child.once('close',closed);child.stdout?.on('data',data);child.stderr?.on('data',diagnostic);
 });
}

export async function stopFixture(child:ChildProcess|undefined,grace=5000):Promise<void>{
 if(!child?.pid||child.exitCode!==null||child.signalCode!==null)return;
 await new Promise<void>((resolve,reject)=>{
  const cleanup=()=>{clearTimeout(force);clearTimeout(deadline);child.off('close',closed);child.off('error',failed)};
  const closed=()=>{cleanup();resolve()};
  const failed=(err:Error)=>{cleanup();reject(err)};
  const force=setTimeout(()=>child.kill('SIGKILL'),grace);
  const deadline=setTimeout(()=>failed(Error('fixture did not close after forced shutdown')),grace+2000);
  child.once('close',closed);child.once('error',failed);child.kill('SIGINT');
 });
}
