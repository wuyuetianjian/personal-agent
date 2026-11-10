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
                    if self.command=='POST' and self.path.endswith('/queue'):
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
            args+=['--session',session] if session else ['--mode','chat_only']
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
        wait_for('chat_only');source=get('/sessions')[0]['id'];output=b'';send('wait');wait_for('model')
        output=b'';send('/queue add first exact');assert committed.wait(5);wait_for('unknown:')
        operation=[w[1] for w in writes if w[0].endswith('/queue')][-1];request=operation['request_id'];assert request.encode() in output
        send('/queue add first exact');time.sleep(.2);assert len([w for w in writes if w[0].endswith('/queue')])==1
        assert len(get('/sessions/'+source+'/snapshot')['runs'])==1
        # Terminating the terminal does not cancel the owned server run or queue.
        proc.terminate();proc.wait(timeout=5);os.close(master);os.close(slave)
        assert get('/sessions/'+source+'/snapshot')['runs'][0]['state']=='running'
        assert len(get('/sessions/'+source+'/queue')['items'])==1
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        output=b'';proc=start(source);wait_for('chat_only');output=b'';send('/queue recover '+source+' '+request);wait_for('No replay.');assert len([w for w in writes if w[0].endswith('/queue')])==1
        state['mode']='normal';output=b'';send('/queue add withdrawn');wait_for('Queue enqueue:');items=get('/sessions/'+source+'/queue')['items'];assert len(items)==2
        output=b'';send('/queue withdraw '+items[1]['id']);wait_for('Queue withdraw:');assert get('/sessions/'+source+'/queue')['items'][1]['state']=='withdrawn'
        output=b'';send('/queue add last');wait_for('Queue enqueue:');assert len(get('/sessions/'+source+'/queue')['items'])==3
        output=b'';send('/queue');wait_for('previous_run:model');assert len(get('/sessions/'+source+'/snapshot')['runs'])==1
        output=b'';send('/cancel');wait_for('completed',20)
        until=time.monotonic()+10
        while time.monotonic()<until:
            snap=get('/sessions/'+source+'/snapshot')
            if len(snap['runs'])==3 and snap['runs'][-1]['state']=='completed':break
            if select.select([master],[],[],.1)[0]:evidence+=os.read(master,65536)
        assert [r['input'] for r in snap['runs']]==['wait','first exact','last'];assert snap['runs'][0]['state']=='cancelled'
        assert Path(info['workspace'],'note.txt').read_text()=='old\n'
        # A character typed into a queue command must never become y/n approval.
        req=urllib.request.Request(info['url']+'/api/v1/sessions',data=json.dumps({'request_id':'queue-approval-session','mode':'default'}).encode(),headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'})
        with urllib.request.urlopen(req) as response:approval_session=json.load(response)['id']
        output=b'';send('/resume '+approval_session);wait_for('已切换会话');output=b'';send('edit');wait_for('需要审批');wait_for('批准 y')
        output=b''
        for ch in '/queue add never approve':os.write(master,ch.encode());time.sleep(.06)
        wait_for('/queue add never approve');time.sleep(.2)
        approved=get('/sessions/'+approval_session+'/snapshot');assert approved['runs'][0]['phase']=='waiting_approval','typing n silently denied the original approval'
        os.write(master,b'\r');wait_for('Queue enqueue:')
        approved=get('/sessions/'+approval_session+'/snapshot');assert approved['runs'][0]['phase']=='waiting_approval','typing n silently denied the original approval';assert approved['runs'][0]['approval']['decision']=='pending'
        assert get('/sessions/'+approval_session+'/queue')['items'][0]['content']=='never approve'
        assert Path(info['workspace'],'note.txt').read_text()=='old\n'
        output=b'';send('/cancel');wait_for('completed',20)
        send('/exit');exit_proc();os.close(slave)
        print('PASS real queue PTY: explicit enqueue, original-ID unknown lock and restart GET recovery, terminal disconnect independent, FIFO, withdrawal vs active cancellation; typed queue text never approves/denies the active tool')
    finally:
        release.set();Path('/tmp/task7d-queue-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if proxy:proxy.shutdown();proxy.server_close()
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
