"""Read/catalog acceptance against one owned seeded metadata-only fixture. Create has separate acceptance."""
import os,pty,subprocess,tempfile,json,select,time,fcntl,termios,struct,sqlite3,urllib.request
with tempfile.TemporaryDirectory(prefix='classification-pty-') as td:
 binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
 env={**os.environ,'PA_AGENT_TOKEN':'classification-local'};server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True);proc=None;master=None
 try:
  assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
  db=sqlite3.connect(info['runtime_parent']+'/runtime-rebuild-v1/agent.db')
  db.execute("INSERT INTO project_execution_classification VALUES('metadata','metadata_only')")
  db.execute("INSERT INTO projects VALUES('metadata','Metadata only','local_private','[]','[]','','[]','[]','[]','{}','[]')");db.commit()
  def get(path):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})))
  master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0))
  proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','default'],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
  output=b''
  def wait_for(term,seconds=12):
   global output
   until=time.monotonic()+seconds
   while time.monotonic()<until:
    if term.encode() in output:return
    if select.select([master],[],[],.1)[0]:output+=os.read(master,65536)
   raise AssertionError(f'missing {term}: {output[-6000:]!r}')
  def wait_dismissed(seconds=12):
   # The idle footer is also rendered underneath a retained result. Wait for the
   # next complete redraw without that result before sending the next command,
   # rather than matching leftover footer bytes and coalescing Enter + command.
   global output
   until=time.monotonic()+seconds
   while time.monotonic()<until:
    frame=output.rsplit(b'\x1b[G',1)[-1]
    if b'\x1b[G' in output and 'Enter 发送'.encode() in frame and b'Metadata rejected' not in frame:return
    if select.select([master],[],[],.1)[0]:output+=os.read(master,65536)
   raise AssertionError('dismissed frame not received: '+repr(output[-2000:]))
  def send(value):os.write(master,value.encode());time.sleep(.15);os.write(master,b'\r')
  wait_for('Enter 发送');sid=get('/sessions')[0]['id'];output=b'';send('/manage projects');wait_for('metadata_only');wait_for('no tools/models/workflows/knowledge isolation enabled')
  os.write(master,b'\x1b');time.sleep(.3);output=b'';send('/project archive metadata');wait_for('Metadata pending');output=b'';os.write(master,b'n');wait_for('Metadata rejected');output=b'';os.write(master,b'\r');wait_dismissed()
  output=b'';send('/project rename metadata Renamed');wait_for('Metadata pending');output=b'';os.write(master,b'y');wait_for('Metadata committed')
  assert db.execute("SELECT execution_class FROM project_execution_classification WHERE project_id='metadata'").fetchone()==('metadata_only',)
  assert db.execute("SELECT name,memory_scope FROM projects WHERE id='metadata'").fetchone()==('Renamed','')
  base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--session',sid,'--format','json']
  r=subprocess.run(base+['--manage','projects'],env=env,capture_output=True,text=True,timeout=15);assert r.returncode==0,r.stderr;record=json.loads(r.stdout)
  assert any(f['name']=='execution_class' and f['value']=='metadata_only' for f in record['items'][0]['fields'])
  assert db.execute('SELECT count(*) FROM workflow_runs').fetchone()==(0,)
  assert len(get('/sessions'))==1
  print('classification PTY: explicit inert classification, cancel/rename and JSON identity passed; directly seeded fixture, create tested separately')
  db.close()
 finally:
  if proc and proc.poll() is None:proc.terminate();proc.wait(timeout=5)
  if master is not None:os.close(master)
  server.send_signal(2);server.wait(timeout=10)
