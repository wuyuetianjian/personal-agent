"""Owned real Go/Ink operator traversal; no providers, user DB or external MCP."""
import os,pty,subprocess,tempfile,json,select,time,fcntl,termios,struct,urllib.request
with tempfile.TemporaryDirectory(prefix='operator-pty-') as td:
 binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
 env={**os.environ,'PA_AGENT_TOKEN':'operator-local'};server=subprocess.Popen([binary,'--operator-fixture'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True);proc=None;master=None
 try:
  assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
  def get(path):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})))
  master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0))
  proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','default'],stdin=slave,stdout=slave,stderr=slave,env=env)
  output=b''
  def wait_for(term,seconds=12):
   global output
   until=time.monotonic()+seconds
   while time.monotonic()<until:
    if term.encode() in output:return
    if select.select([master],[],[],.1)[0]:output+=os.read(master,65536)
   raise AssertionError(f'missing {term}: {output[-4000:]!r}')
  def send(value):os.write(master,value.encode());time.sleep(.15);os.write(master,b'\r')
  wait_for('Enter 发送');sid=get('/sessions')[0]['id'];output=b'';send('edit');wait_for('new 你好');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';send('/op rename project-000 <img src=x> exact name');wait_for('Metadata pending');wait_for('Old:');wait_for('New:');os.write(master,b'n');output=b'';wait_for('Metadata rejected');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');until=time.monotonic()+.5
  while time.monotonic()<until:
   if select.select([master],[],[],.05)[0]:output+=os.read(master,65536)
  output=b'';send('/op rename project-000 <img src=x> exact name');wait_for('Metadata pending');output=b'';os.write(master,b'y');wait_for('Metadata committed');output=b'';os.write(master,b'y');until=time.monotonic()+1
  while time.monotonic()<until:
   if select.select([master],[],[],.1)[0]:output+=os.read(master,65536)
  assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval','second metadata y approved unrelated tool'
  notice=get('/sessions/'+sid+'/operator/notifications')['items'][0]['id']
  base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--session',sid,'--format','json']
  def cli(args):
   r=subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=15);assert r.returncode==0,r.stderr;return json.loads(r.stdout)
  p=cli(['--op','read','--target',notice,'--request-id','cli-preview']);assert p['state']=='pending'
  d=cli(['--op','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','cli-confirm']);assert d['state']=='committed'
  assert cli(['--op','lookup','--request-id','cli-confirm'])==d
  controls='control\x7f\x85\x9b literal\\u{85}'
  cp=cli(['--op','rename','--target','project-000','--new-name',controls,'--request-id','cli-controls'])
  text=subprocess.run(base[:-2]+['--format','text','--op','lookup','--request-id','cli-controls'],env=env,capture_output=True,text=True,timeout=15)
  assert text.returncode==0,text.stderr
  assert 'New: "control\\u{7f}\\u{85}\\u{9b} literal\\\\u{85}"' in text.stdout,text.stdout
  assert cp['new_value']==controls
  output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');until=time.monotonic()+.5
  while time.monotonic()<until:
   if select.select([master],[],[],.05)[0]:output+=os.read(master,65536)
  output=b'';send('/op recover '+sid+' cli-controls');wait_for('New: "control\\u{7f}\\u{85}\\u{9b} literal\\\\u{85}"')
  assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  assert len(get('/sessions/'+sid+'/snapshot')['runs'])==1
  print('operator actions PTY: exact preview/cancel/confirm, repeated y isolation and JSON CLI passed')
 finally:
  if proc and proc.poll() is None:proc.terminate();proc.wait(timeout=5)
  if master is not None:os.close(master)
  server.send_signal(2);server.wait(timeout=10)
