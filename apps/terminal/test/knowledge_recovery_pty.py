"""Real Ink + Go with an owned loopback proxy delaying/dropping only responses.
The proxy forwards exactly one authenticated POST; it never fabricates a commit.
"""
import http.client
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='queue-pty-') as td:
    binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'recovery-pty'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;proxy=None;evidence=b'';output=b'';release=threading.Event();committed=threading.Event();state={'mode':'lost'};writes=[]
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
                    if self.command=='POST' and self.path.endswith('/knowledge/decision'):
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
        wait_for('Enter 发送');source=get('/sessions')[0]['id'];output=b'';send('/knowledge text '+json.dumps({'label':'owned \u202e\x85','content':'orchid exact local evidence'}));wait_for('Knowledge pending');wait_for('\\u{202e}\\u{85}');output=b'';os.write(master,b'y');assert committed.wait(5);wait_for('unknown:')
        operation=[w[1] for w in writes if w[0].endswith('/knowledge/decision')][-1];request=operation['request_id'];assert request.encode() in output
        os.write(master,b'yy\x0f');time.sleep(.2);assert len([w for w in writes if w[0].endswith('/knowledge/decision')])==1
        output=b'';os.write(master,b'r');wait_for('Knowledge committed');assert not get('/sessions/'+source+'/snapshot')['runs']
        proc.terminate();proc.wait(timeout=5);os.close(master);os.close(slave)
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0));output=b'';proc=start(source);wait_for('Enter 发送');output=b'';send('/knowledge recover '+source+' '+request);wait_for('Knowledge committed')
        assert len([w for w in writes if w[0].endswith('/knowledge/decision')])==1
        base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',proxy_url,'--session',source,'--format','json']
        def cli(args):return subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=15)
        p=cli(['--knowledge','file','--file','note.txt','--request-id','print-preview']);assert p.returncode==0,p.stderr;p=json.loads(p.stdout);state['mode']='lost'
        r=cli(['--knowledge','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','print-decision']);assert r.returncode==2,r.stderr;unknown=json.loads(r.stdout);assert unknown['state']=='unknown' and unknown['request_id']=='print-decision'
        r=cli(['--knowledge','lookup','--request-id','print-decision']);assert r.returncode==0,r.stderr;assert json.loads(r.stdout)['state']=='committed';assert len([w for w in writes if w[0].endswith('/knowledge/decision')])==2
        assert not get('/sessions/'+source+'/snapshot')['runs']
        r=cli(['--knowledge','search','--query','orchid']);assert r.returncode==0,r.stderr;result=json.loads(r.stdout);assert result['engine']=='local_bm25' and len(result['matches'])==1;doc=result['matches'][0]['document_id']
        r=cli(['--knowledge','search','--query','nomatchtoken']);assert not json.loads(r.stdout)['matches']
        for action in ['deactivate','reindex']:
            r=cli(['--knowledge',action,'--document',doc,'--request-id',action+'-p']);assert r.returncode==0,r.stderr;p=json.loads(r.stdout)
            r=cli(['--knowledge','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id',action+'-d']);assert r.returncode==0,r.stderr
            r=cli(['--knowledge','search','--query','orchid']);assert bool(json.loads(r.stdout)['matches'])==(action=='reindex')
        r=cli(['--knowledge','file','--file','note.txt','--request-id','changed-p']);p=json.loads(r.stdout);Path(info['workspace'],'note.txt').write_text('changed file')
        r=cli(['--knowledge','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','changed-d']);assert r.returncode==1,r.stderr;assert 'changed' in json.loads(r.stdout)['reason']
        assert len(get('/sessions'))==1 and not get('/sessions/'+source+'/snapshot')['runs']
        print('knowledge recovery PTY: real committed ack loss, unknown quarantine, GET recovery, reopen, stdout-clean CLI passed')
    finally:
        release.set();Path('/tmp/task7e3b-knowledge-recovery-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if proxy:proxy.shutdown();proxy.server_close()
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
