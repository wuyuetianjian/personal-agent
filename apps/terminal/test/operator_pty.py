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
  wait_for('default');sid=get('/sessions')[0]['id'];output=b'';send('edit');wait_for('new 你好');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';send('/manage tasks');wait_for('Bidi identity fixture');wait_for('task-evil-isolate');assert not any(chr(c).encode() in output for c in list(range(0x202a,0x202f))+list(range(0x2066,0x206a))), 'bidi formatting leaked into Ink from exact ID'
  exact_id='task-\u202eevil\u202c-\u2066isolate\u2069';assert any(row['id']==exact_id for row in get('/sessions/'+sid+'/operator/tasks')['items']), 'wire identity must remain exact'
  os.write(master,b'\x1b');output=b'';wait_for('new 你好');output=b'';send('/manage projects');wait_for('READ ONLY');wait_for('legacy data not imported');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';os.write(master,b'\x1b');wait_for('new 你好');os.write(master,'保留草稿'.encode());time.sleep(.2)
  output=b'';os.write(master,b'\x0f');wait_for('READ ONLY');output=b'';os.write(master,b'\t');wait_for('knowledge');wait_for('Only current immutable');output=b'';os.write(master,b'\x1b[D');wait_for('projects');wait_for('Project IDs are metadata')
  output=b'';os.write(master,b'\x1b');wait_for('保留草稿');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  # Noninteractive read command requires an existing session and emits one JSON value.
  result=subprocess.run(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--session',sid,'--manage','diagnostics','--format','json'],env=env,capture_output=True,text=True,timeout=15)
  assert result.returncode==0,result.stderr;data=json.loads(result.stdout);assert data['resource']=='diagnostics' and data['namespace']=='runtime-rebuild-v1'
  assert len(get('/sessions/'+sid+'/snapshot')['runs'])==1
  print('operator PTY: read navigation, live approval, draft, JSON purity passed')
 finally:
  if proc and proc.poll() is None:proc.terminate();proc.wait(timeout=5)
  if master is not None:os.close(master)
  server.send_signal(2);server.wait(timeout=10)
