"""Real Ink + Go with an owned loopback proxy delaying/dropping only responses.
The proxy forwards exactly one authenticated POST; it never fabricates a commit.
"""
import http.client
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='queue-pty-') as td:
    binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'recovery-pty','XDG_STATE_HOME':td+'/state'}
    manifest=json.loads(Path('protocol/v1/skills.golden.json').read_text())['manifest_text'];manifest_path=td+'/literal manifest.yaml';Path(manifest_path).write_text('\ufeff'+manifest)
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;proxy=None;evidence=b'';output=b'';release=threading.Event();committed=threading.Event();state={'mode':'normal'};writes=[]
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
                    if self.command=='POST' and self.path.endswith('/skills/'+state.get('endpoint','decision')):
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
            Path('/tmp/skill-pty-debug.log').write_bytes(evidence);raise AssertionError(f'missing {term}: {output[-1000:]!r}')
        def send(value):
            global output,evidence
            os.write(master,value.encode());until=time.monotonic()+.15
            while time.monotonic()<until:
                if select.select([master],[],[],.01)[0]:
                    data=os.read(master,65536);output+=data;evidence+=data
            os.write(master,b'\r')
        def exit_proc():
            until=time.monotonic()+10
            while proc.poll() is None and time.monotonic()<until:
                if select.select([master],[],[],.1)[0]:
                    try:os.read(master,65536)
                    except OSError:break
            proc.wait(timeout=.1);assert proc.returncode==0
        wait_for('Enter 发送');source=get('/sessions')[0]['id'];output=b'';send('edit');wait_for('批准 y / 拒绝 n');output=b'';send('/skills import-file '+manifest_path);wait_for('Skill metadata / 技能目录');wait_for('Skill catalog metadata review: pending');output=b'';os.write(master,b'/skills cancel\r');wait_for('rejected');os.write(master,b'yy');time.sleep(.2);assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        output=b'';os.write(master,b'\r');wait_for('Skill result dismissed.');os.write(master,b'yy\r\r');time.sleep(.2);assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval';output=b'';send('/skills import-file '+manifest_path);wait_for('Skill catalog metadata review: pending');state['mode']='lost';committed.clear();output=b'';os.write(master,b'y');wait_for('unknown:');assert committed.is_set();operation=[w[1] for w in writes if w[0].endswith('/skills/decision')][-1];request=operation['request_id'];os.write(master,b'yy\x0f');time.sleep(.2);assert len([w for w in writes if w[0].endswith('/skills/decision')])==2
        # Restart while UNKNOWN, not after resolution: durable ID-only quarantine.
        proc.kill();proc.wait(timeout=5);os.close(master);os.close(slave)
        saved=list(Path(td+'/state/personal-agent').glob('skill-*.json'));assert len(saved)==1;record=json.loads(saved[0].read_text());assert sorted(record)==['operation_id','request_id','session_id'];assert record['request_id']==request
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0));output=b'';proc=start();wait_for('unknown:');assert len(get('/sessions'))==1,'restored skill recovery created hidden session';os.write(master,b'yy');time.sleep(.2);assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval';output=b'';os.write(master,b'r');wait_for('committed');os.write(master,b'yy');time.sleep(.2);assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval';assert len([w for w in writes if w[0].endswith('/skills/decision')])==2
        output=b'';send('/skills dismiss  ');wait_for('Skill result dismissed.');os.write(master,b'y');time.sleep(.2);assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval','dismiss command leaked approval';output=b'';send('/skills');wait_for('disabled');catalog=get('/sessions/'+source+'/skills')['items'][0];versions=get('/sessions/'+source+'/skills/'+catalog['id']+'/versions');revision=versions['items'][0]['id']
        output=b'';send('/skills enable '+catalog['id']+' '+revision);wait_for('Skill catalog metadata review: pending');output=b'';os.write(master,b'y');wait_for('committed');assert get('/sessions/'+source+'/skills/'+catalog['id'])['selected_revision']==revision
        output=b'';os.write(master,b'\r');wait_for('Skill result dismissed.');time.sleep(.2);output=b'';send('/skills disable '+catalog['id']);wait_for('Skill catalog metadata review: pending');output=b'';os.write(master,b'y');wait_for('committed');assert get('/sessions/'+source+'/skills/'+catalog['id'])['state']=='disabled'
        output=b'';os.write(master,b'\r');wait_for('Skill result dismissed.');time.sleep(.2)
        state['endpoint']='preview';state['mode']='lost';output=b'';send('/skills import-file '+manifest_path);wait_for('unknown:');count=len(writes);proc.kill();proc.wait(timeout=5);os.close(master);os.close(slave)
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0));output=b'';proc=start();wait_for('unknown:');assert len(get('/sessions'))==1,'restored skill recovery created hidden session';output=b'';os.write(master,b'r');wait_for('Skill catalog metadata review: pending');assert len(writes)==count;output=b'';os.write(master,b'n');wait_for('rejected');assert get('/sessions/'+source+'/snapshot')['runs'][0]['phase']=='waiting_approval'
        base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',proxy_url,'--session',source,'--format','json']
        def cli(args):return subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=15)
        Path(manifest_path).write_text(manifest.replace('\"1.0\"','\"2.0\"'));state['endpoint']='decision';state['mode']='normal';result=cli(['--skill','import','--manifest-file',manifest_path,'--request-id','machine-p']);assert result.returncode==0,result.stderr;preview=json.loads(result.stdout);state['mode']='lost';result=cli(['--skill','confirm','--operation-id','machine-p','--digest',preview['digest'],'--request-id','machine-d']);assert result.returncode==2,result.stderr;unknown=json.loads(result.stdout);assert unknown['state']=='unknown';assert unknown['recovery_args']==['--url',proxy_url,'--skill','lookup','--session',source,'--request-id','machine-d','--format','json'];count=len(writes);result=cli(['--skill','lookup','--request-id','machine-d']);assert result.returncode==0,result.stderr;assert json.loads(result.stdout)['state']=='committed';assert len(writes)==count
        print('skills PTY: import/cancel, exact enable/disable, decision and prepare ACK loss across restart, ID-only disk recovery, original GET, repeated y isolated from live approval')
    finally:
        release.set()
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=5)
        if master is not None:os.close(master)
        if proxy:proxy.shutdown();proxy.server_close()
        server.send_signal(signal.SIGINT);server.wait(timeout=10)
