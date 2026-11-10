"""Owned real Go/Ink operator traversal; no providers, user DB or external MCP."""
import os,pty,subprocess,tempfile,json,select,time,fcntl,termios,struct,urllib.request
with tempfile.TemporaryDirectory(prefix='operator-pty-') as td:
 binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
 env={**os.environ,'PA_AGENT_TOKEN':'operator-local'};server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True);proc=None;master=None
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
  def post(path,body):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'Content-Type':'application/json','X-Agent-Protocol':'personal-agent-rebuild-v1'})))
  post('/sessions/'+sid+'/queue',{'request_id':'queue-owned','content':'queued fixture'})
  post('/sessions/'+sid+'/jobs',{'request_id':'job-owned','content':'owned job','effects':False,'origin_run_id':''})
  output=b'';send('/knowledge text '+json.dumps({'label':'owned \u202e\x85','content':'orchid <img src=x> exact evidence'}));wait_for('Knowledge pending');wait_for('\\u{202e}\\u{85}');wait_for('Excerpt');os.write(master,b'n');output=b'';wait_for('Knowledge rejected');assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');until=time.monotonic()+.5
  while time.monotonic()<until:
   if select.select([master],[],[],.05)[0]:output+=os.read(master,65536)
  output=b'';send('/knowledge text '+json.dumps({'label':'owned \u202e\x85','content':'orchid <img src=x> exact evidence'}));wait_for('Knowledge pending');output=b'';os.write(master,b'y');wait_for('Knowledge committed');output=b'';os.write(master,b'y');until=time.monotonic()+1
  while time.monotonic()<until:
   if select.select([master],[],[],.1)[0]:output+=os.read(master,65536)
  assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval','second metadata y approved unrelated tool'
  assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');time.sleep(.2);output=b'';send('/knowledge search orchid');wait_for('Search results: actual local BM25');wait_for('orchid')
  output=b'';send('/knowledge file note.txt');wait_for('Knowledge pending');wait_for('Source (file):');os.write(master,b'n');output=b'';wait_for('Knowledge rejected');os.write(master,b'y');time.sleep(.2);assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');time.sleep(.2);os.write(master,'knowledge preserved draft'.encode());time.sleep(.15);os.write(master,b'\x0f');output=b'';wait_for('READ ONLY');os.write(master,b'\x1b');output=b'';wait_for('knowledge preserved draft')
  assert get('/sessions/'+sid+'/snapshot')['runs'][0]['phase']=='waiting_approval'
  assert len(get('/sessions/'+sid+'/snapshot')['runs'])==1
  assert get('/sessions/'+sid+'/queue')['items'][0]['state']=='pending'
  assert len(get('/sessions/'+sid+'/jobs')['items'])==1
  print('knowledge actions PTY: text/file preview cancel/confirm, local search, hostile controls, repeated y isolation, live approval/queue/Job/draft preserved')
 finally:
  if proc and proc.poll() is None:proc.terminate();proc.wait(timeout=5)
  if master is not None:os.close(master)
  server.send_signal(2);server.wait(timeout=10)
