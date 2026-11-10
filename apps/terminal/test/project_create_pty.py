"""Real Ink + Go with an owned loopback proxy delaying/dropping only responses.
The proxy forwards exactly one authenticated POST; it never fabricates a commit.
"""
import http.client, sqlite3
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='create-pty-') as td:
    binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'recovery-pty'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;proxy=None;evidence=b'';output=b'';release=threading.Event();committed=threading.Event();state={'mode':'normal','preview_mode':'normal'};writes=[]
    try:
        assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
        def get(path):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})))
        class Proxy(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_GET(self):self.forward()
            def do_POST(self):self.forward()
            def forward(self):
                body=self.rfile.read(int(self.headers.get('Content-Length','0'))) if self.command=='POST' else None
                req=urllib.request.Request(info['url']+self.path,data=body,method=self.command,headers={k:v for k,v in self.headers.items() if k.lower() not in ['host','connection','content-length']})
                try:response=urllib.request.urlopen(req,timeout=30)
                except urllib.error.HTTPError as e:response=e
                try:
                    if self.command=='POST':writes.append((self.path,json.loads(body)))
                    if self.command=='POST' and self.path.endswith('/operator-actions/preview') and state['preview_mode']=='lost':
                        response.read();state['preview_mode']='normal';self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);return
                    if self.command=='POST' and self.path.endswith('/operator-actions/decision'):
                        payload=response.read();mode=state['mode'];state['mode']='normal';committed.set()
                        if mode=='delay':assert release.wait(15),'test did not release delayed response'
                        if mode=='lost':self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);return
                        self.send_response(response.status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(payload)));self.end_headers();self.wfile.write(payload);return
                    self.send_response(response.status)
                    for key in ['Content-Type','Content-Length']:
                        if response.headers.get(key):self.send_header(key,response.headers[key])
                    self.end_headers()
                    while True:
                        chunk=response.read1(65536)
                        if not chunk:break
                        self.wfile.write(chunk);self.wfile.flush()
                except (BrokenPipeError,ConnectionResetError,TimeoutError,http.client.IncompleteRead):pass
                finally:response.close()
        proxy=ThreadingHTTPServer(('127.0.0.1',0),Proxy);proxy.daemon_threads=True;threading.Thread(target=proxy.serve_forever,daemon=True).start()
        proxy_url='http://127.0.0.1:'+str(proxy.server_port)
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        def start(session=None):
            args=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',proxy_url]
            args+=['--session',session] if session else ['--mode','default']
            return subprocess.Popen(args,stdin=slave,stdout=slave,stderr=slave,env=env)
        proc=start()
        def wait_for(term,seconds=15):
            global output,evidence
            until=time.monotonic()+seconds
            while time.monotonic()<until:
                if term.encode() in output:return
                if select.select([master],[],[],.1)[0]:
                    try:data=os.read(master,65536)
                    except OSError:break
                    output+=data;evidence+=data
            raise AssertionError(f'missing {term}: {output[-8000:]!r}')
        def send(value):os.write(master,value.encode());time.sleep(.15);os.write(master,b'\r')
        def exit_proc():
            until=time.monotonic()+10
            while proc.poll() is None and time.monotonic()<until:
                if select.select([master],[],[],.1)[0]:
                    try:os.read(master,65536)
                    except OSError:break
            proc.wait(timeout=.1);assert proc.returncode==0
        wait_for('Enter 发送');source=get('/sessions')[0]['id'];db=sqlite3.connect(info['runtime_parent']+'/runtime-rebuild-v1/agent.db')
        output=b'';send('edit');wait_for('new 你好');assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        output=b'';send('/project create Cancelled project');wait_for('Metadata pending');wait_for('metadata_only');assert db.execute('SELECT count(*) FROM projects').fetchone()==(0,)
        output=b'';os.write(master,b'n');wait_for('Metadata rejected');assert db.execute('SELECT count(*) FROM projects').fetchone()==(0,)
        output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');time.sleep(.5)
        output=b'';send('/project create Created <img src=x> exact name');wait_for('Metadata pending');wait_for('no tools/models/workflows/knowledge isolation enabled');assert db.execute('SELECT count(*) FROM projects').fetchone()==(0,)
        state['mode']='lost';committed.clear();output=b'';os.write(master,b'y');wait_for('unknown:');assert committed.is_set()
        operation=[w[1] for w in writes if w[0].endswith('/operator-actions/decision')][-1];request=operation['request_id'];assert request.encode() in output
        os.write(master,b'yy\x0f');time.sleep(.3);assert len([w for w in writes if w[0].endswith('/operator-actions/decision')])==2
        assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        assert db.execute('SELECT count(*) FROM projects').fetchone()==(1,)
        assert db.execute("SELECT count(*) FROM security_audit_log WHERE action='project.create' AND status='committed'").fetchone()==(1,)
        proc.terminate();proc.wait(timeout=5);os.close(master);os.close(slave)
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0));output=b'';proc=start(source);wait_for('Enter 发送');output=b'';send('/op recover '+source+' '+request);wait_for('Metadata committed');wait_for('metadata_only')
        assert len([w for w in writes if w[0].endswith('/operator-actions/decision')])==2
        target=get('/sessions/'+source+'/operator-actions/'+request)['target_id'];assert target!=operation['operation_id']
        for action in ['rename '+target+' Renamed created project','archive '+target]:
            output=b'';os.write(master,b'\r');wait_for('批准 y / 拒绝 n');time.sleep(.5);output=b'';send('/project '+action);wait_for('Metadata pending');output=b'';os.write(master,b'y');wait_for('Metadata committed');os.write(master,b'y');time.sleep(.3)
            assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        assert db.execute('SELECT execution_class FROM project_execution_classification WHERE project_id=?',(target,)).fetchone()==('metadata_only',)
        assert db.execute('SELECT name,memory_scope FROM projects WHERE id=?',(target,)).fetchone()==('Renamed created project','')
        assert db.execute('SELECT state FROM project_catalog_lifecycle WHERE project_id=?',(target,)).fetchone()==('archived',)
        base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',proxy_url,'--session',source,'--format','json']
        def cli(args):return subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=15)
        controls='control\x7f\x85\x9b literal\\u{85}'
        r=cli(['--op','create','--new-name',controls,'--request-id','print-preview']);assert r.returncode==0,r.stderr;p=json.loads(r.stdout);assert p['new_value']==controls and p['execution_class']=='metadata_only' and p['target_id']!=p['operation_id'];state['mode']='lost'
        r=cli(['--op','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','print-decision']);assert r.returncode==2,r.stderr;unknown=json.loads(r.stdout);assert unknown['state']=='unknown' and unknown['request_id']=='print-decision' and unknown['operation_id']=='print-preview' and unknown['recovery_request_id']=='print-decision'
        r=cli(['--op','lookup','--request-id','print-decision']);assert r.returncode==0,r.stderr;done=json.loads(r.stdout);assert done['state']=='committed' and done['target_id']==p['target_id']
        assert db.execute('SELECT count(*) FROM projects').fetchone()==(2,)
        assert db.execute("SELECT count(*) FROM security_audit_log WHERE action='project.create' AND status='committed'").fetchone()==(2,)
        text=subprocess.run(base[:-2]+['--format','text','--op','lookup','--request-id','print-decision'],env=env,capture_output=True,text=True,timeout=15);assert text.returncode==0,text.stderr;assert 'New: "control\\u{7f}\\u{85}\\u{9b} literal\\\\u{85}"' in text.stdout,text.stdout
        r=cli(['--op','create','--new-name','x'*2049,'--request-id','refused']);assert r.returncode==1;refused=json.loads(r.stdout);assert refused['state']=='refused' and refused['operation_id']=='' and refused['recovery_request_id']==''
        state['preview_mode']='lost';r=cli(['--op','create','--new-name','Preview lost','--request-id','preview-lost']);assert r.returncode==2;unknown=json.loads(r.stdout);assert unknown['state']=='unknown' and unknown['recovery_request_id']=='preview-lost'
        r=cli(['--op','lookup','--request-id','preview-lost']);assert r.returncode==0;p=json.loads(r.stdout);assert p['state']=='pending'
        r=cli(['--op','confirm','--operation-id',p['operation_id'],'--digest','0'*64,'--request-id','wrong-decision']);assert r.returncode==2;refused=json.loads(r.stdout);assert refused['state']=='unresolved' and refused['request_id']=='wrong-decision' and refused['recovery_request_id']=='preview-lost'
        r=cli(['--op','lookup','--request-id','preview-lost']);assert r.returncode==0 and json.loads(r.stdout)['state']=='pending'
        assert len([w for w in writes if w[0].endswith('/operator-actions/preview') and w[1]['request_id']=='preview-lost'])==1
        assert db.execute('SELECT count(*) FROM projects').fetchone()==(2,)
        assert len(get('/sessions'))==1 and len(get('/sessions/'+source+'/snapshot')['runs'])==1
        assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        print('create PTY: cancel/confirm/rename/archive, repeated y isolation, real lost commit + reopen GET recovery, machine IDs/exits/hostile controls/refusal/lost preview all passed')
        db.close()
    finally:
        release.set();Path('/tmp/task7e5b-create-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if proxy:proxy.shutdown();proxy.server_close()
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
