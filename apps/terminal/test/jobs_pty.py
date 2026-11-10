"""Real Ink + Go with an owned loopback proxy delaying/dropping only responses.
The proxy forwards exactly one authenticated POST; it never fabricates a commit.
"""
import http.client
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='jobs-pty-') as td:
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
                    if self.command=='POST' and self.path.endswith('/jobs'):
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
        wait_for('default');source=get('/sessions')[0]['id'];output=b'';send('wait');wait_for('model')
        output=b'';send('/jobs add wait');assert committed.wait(5);wait_for('unknown:')
        operation=[w[1] for w in writes if w[0].endswith('/jobs')][-1];request=operation['request_id'];assert request.encode() in output
        send('/jobs add wait');time.sleep(.2);assert len([w for w in writes if w[0].endswith('/jobs')])==1
        assert len(get('/sessions/'+source+'/snapshot')['runs'])==1
        proc.terminate();proc.wait(timeout=5);os.close(master);os.close(slave)
        assert get('/sessions/'+source+'/snapshot')['runs'][0]['state']=='running'
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        output=b'';proc=start(source);wait_for('default');output=b'';send('/jobs recover '+source+' '+request);wait_for('No replay.');assert len([w for w in writes if w[0].endswith('/jobs')])==1
        jobs=get('/sessions/'+source+'/jobs')['items'];job=jobs[0];output=b'';send('/jobs stop '+job['id']);wait_for('Job stop:')
        def job_state(index,state):
            until=time.monotonic()+10
            while time.monotonic()<until:
                jobs=get('/sessions/'+source+'/jobs')['items']
                if len(jobs)>index and jobs[index]['state']==state:return jobs[index]
                if select.select([master],[],[],.05)[0]:os.read(master,65536)
            raise AssertionError((state,jobs))
        job_state(0,'stopped');assert get('/sessions/'+source+'/snapshot')['runs'][0]['state']=='running'
        state['mode']='normal';output=b'';send('/jobs add edit');wait_for('Job create:');job_state(1,'failed');assert Path(info['workspace'],'note.txt').read_text()=='old\n'
        output=b'';send('/jobs edit edit');wait_for('Job create:');edit=job_state(2,'waiting_approval');output=b'';send('/jobs show '+edit['id']);wait_for('exact_review');wait_for('new 你好')
        assert edit['run_id'].encode() in output;assert edit['session_id'].encode() in output
        output=b'';send('/jobs deny '+edit['id']);wait_for('Job decision:');job_state(2,'failed');assert Path(info['workspace'],'note.txt').read_text()=='old\n'
        output=b'';send('/jobs edit edit');wait_for('Job create:');edit=job_state(3,'waiting_approval');output=b'';send('/jobs show '+edit['id']);wait_for('exact_review');output=b'';send('/jobs approve '+edit['id']);wait_for('Job decision:');job_state(3,'completed');assert Path(info['workspace'],'note.txt').read_text()=='new 你好\n'
        Path(info['workspace'],'large.txt').write_text('owned artifact content 中文\n'*400)
        output=b'';send('/jobs add artifact-read');wait_for('Job create:');artifact=job_state(4,'completed');detail=get('/sessions/'+source+'/jobs/'+artifact['id']);ref=json.loads(detail['snapshot']['runs'][0]['tools'][0]['result'])['artifact']['artifact_id'];output=b'';send('/jobs artifact '+artifact['id']+' '+ref);wait_for('Untrusted Job artifact');wait_for('owned artifact content')
        assert len(get('/sessions/'+source+'/snapshot')['runs'])==1
        output=b'';send('/jobs origin');wait_for('unchanged');output=b'';send('/exit');exit_proc();assert get('/sessions/'+source+'/snapshot')['runs'][0]['state']=='running'
        print('PASS Jobs Ink PTY: immutable busy origin, lost ack/restart GET recovery, separate stop, read-only refusal, native exact denial/approval, UI exit independence')
    finally:
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=5)
        if master is not None:
            try:os.close(master)
            except OSError:pass
        if proxy:proxy.shutdown();proxy.server_close()
        server.send_signal(signal.SIGINT)
        try:server.wait(timeout=10)
        except subprocess.TimeoutExpired:server.kill();server.wait()
