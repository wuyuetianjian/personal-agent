"""Real Ink + Go with an owned loopback proxy delaying/dropping only responses.
The proxy forwards exactly one authenticated POST; it never fabricates a commit.
"""
import http.client, sqlite3
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='context-recovery-pty-') as td:
    binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'recovery-pty'}
    server=subprocess.Popen([binary,'--safe-mcp'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;proxy=None;evidence=b'';output=b'';release=threading.Event();committed=threading.Event();state={'mode':'normal'};writes=[]
    try:
        assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
        def get(path):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})))
        class Proxy(BaseHTTPRequestHandler):
            def log_message(self,*args):pass
            def do_GET(self):self.forward()
            def do_POST(self):self.forward()
            def forward(self):
                if self.command=='GET' and self.path=='/api/v1/protocol' and state.get('protocol_fail'):
                    state['protocol_fail']=False;self.send_response(404);self.send_header('Content-Length','0');self.end_headers();return
                if self.command=='GET' and self.path==state.get('admission_override_path'):
                    state.pop('admission_override_path');payload=json.dumps(state['admission_override']).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(payload)));self.end_headers();self.wfile.write(payload);return
                body=self.rfile.read(int(self.headers.get('Content-Length','0'))) if self.command=='POST' else None
                req=urllib.request.Request(info['url']+self.path,data=body,method=self.command,headers={k:v for k,v in self.headers.items() if k.lower() not in ['host','connection','content-length']})
                try:response=urllib.request.urlopen(req,timeout=30)
                except urllib.error.HTTPError as e:response=e
                try:
                    if self.command=='POST':writes.append((self.path,json.loads(body)))
                    if self.command=='POST' and self.path.endswith('/messages') and state.get('drop_message'):
                        state['drop_message']=False;response.read();self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);return
                    if self.command=='POST' and self.path=='/api/v1/sessions':
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
        wait_for('chat_only');parent=get('/sessions')[0]['id']
        db=sqlite3.connect(info['runtime_parent']+'/runtime-rebuild-v1/agent.db')
        db.execute("INSERT INTO project_execution_classification VALUES('metadata','metadata_only')")
        db.execute("INSERT INTO projects VALUES('metadata','Original label','local_private','[]','[]','','[]','[]','[]','{}','[]')");db.commit()
        hostile_id='hostile\u202e\u2028\u2029\u0085'+r'\u202e';hostile_name='Hostile name\u2066\u2028'+r'\u2066'
        db.execute('INSERT INTO project_execution_classification VALUES(?,?)',(hostile_id,'metadata_only'))
        db.execute("INSERT INTO projects VALUES(?,?,'local_private','[]','[]','','[]','[]','[]','{}','[]')",(hostile_id,hostile_name));db.commit()
        output=b'';send('/new labels');wait_for('Active labels only');assert len(get('/sessions'))==1
        assert not any(c.encode() in output for c in ['\u202e','\u2028','\u2029','\u0085','\u2066']),repr(output[-4000:])
        assert json.dumps(hostile_id).encode() in output,repr(output[-4000:])
        output=b'';send('/new label metadata');wait_for('Next-session only');wait_for('Original label');assert get('/sessions/'+parent+'/snapshot')['session'].get('project_label') is None
        output=b'';send('/new label none');wait_for('label cleared')
        output=b'';send('/new label metadata');wait_for('Next-session only')
        committed.clear();state['mode']='lost';output=b'';send('/new create confirm default mcp');assert committed.wait(10);wait_for('unknown:')
        request=[w[1] for w in writes if w[0]=='/api/v1/sessions'][-1]['request_id'];assert request.encode() in output
        os.write(master,b'yyy');time.sleep(.2);assert len(get('/sessions'))==2;assert len([w for w in writes if w[0]=='/api/v1/sessions'])==2
        assert Path(info['workspace'],'mcp-marker.txt.launch').read_text().splitlines()==['launch']
        output=b'';os.write(master,b'r');wait_for('Admission recovered read-only');wait_for('Original label');assert len(get('/sessions'))==2
        child=get('/sessions')[0]['id'];assert get('/sessions/'+child+'/snapshot')['runs']==[]
        output=b'';send('hello label');wait_for('completed');output=b'';send('/branch latest');wait_for('Parent');branch=get('/sessions')[0]['id'];assert branch!=child;assert get('/sessions/'+branch+'/snapshot')['session']['project_label']['name']=='Original label'
        db.execute("UPDATE projects SET name='Changed catalog' WHERE id='metadata'");db.execute("UPDATE project_catalog_lifecycle SET state='archived',revision=revision+1 WHERE project_id='metadata'");db.commit()
        output=b'';send('/history');wait_for('/resume 会话ID 切换');output=b'';send('/jobs add owned job');wait_for('Job create');time.sleep(.5)
        jobs=get('/sessions/'+branch+'/jobs')['items'];assert len(jobs)==1;job=jobs[0];assert get('/sessions/'+branch+'/jobs/'+job['id'])['snapshot']['session']['project_label']['name']=='Original label'
        output=b'';send('/jobs show '+job['id']);wait_for('"project_label"');wait_for('Original label')
        output=b'';send('/new label none');wait_for('label cleared');output=b'';send('/new create confirm chat_only');wait_for('New empty session');latest=get('/sessions')[0];assert latest.get('project_label') is None
        output=b'';send('/new label-json '+json.dumps(hostile_id));wait_for('Next-session only')
        output=b'';send('/new create confirm chat_only');wait_for('New empty session');hostile_session=get('/sessions')[0]['id']
        assert get('/sessions/'+hostile_session+'/snapshot')['session']['project_label']['project_id']==hostile_id
        output=b'';send('/jobs add hostile provenance');wait_for('Job create');time.sleep(.5)
        hostile_job=get('/sessions/'+hostile_session+'/jobs')['items'][0]
        output=b'';send('/jobs show '+hostile_job['id']);wait_for('/jobs artifact')
        assert not any(c.encode() in output for c in ['\u202e','\u2028','\u2029','\u0085','\u2066']),repr(output[-4000:])
        assert json.dumps(hostile_id).encode() in output,repr(output[-4000:])
        assert get('/sessions/'+hostile_session+'/jobs/'+hostile_job['id'])['snapshot']['session']['project_label']['project_id']==hostile_id
        # An owned old-server preflight refusal must not send a session POST or lock Ink.
        before_posts=len(writes);state['protocol_fail']=True;output=b'';send('/new create confirm chat_only');wait_for('session admission not sent');assert len(writes)==before_posts
        output=b'';send('/new create confirm chat_only');wait_for('New empty session');assert len(writes)==before_posts+1
        # Same-request success is already applied to the active controller. A
        # read-only recovery must not leave confirmed admission quarantine stuck.
        same_request=[w[1] for w in writes if w[0]=='/api/v1/sessions'][-1]['request_id']
        same_session=get('/sessions')[0]['id']
        same_session_posts=len([w for w in writes if w[0]=='/api/v1/sessions']);same_launches=Path(info['workspace'],'mcp-marker.txt.launch').read_text()
        output=b'';send('/new recover '+same_request);wait_for('Admission recovered read-only')
        output=b'';send('same-request recovery permits input')
        try:wait_for('completed',seconds=5)
        except AssertionError:
            assert get('/sessions/'+same_session+'/snapshot')['runs']==[]
            raise AssertionError('Same-request recovery stayed confirmed/locked on the already active session; explicit message produced no run')
        same_runs=get('/sessions/'+same_session+'/snapshot')['runs'];assert len(same_runs)==1;assert same_runs[0]['input']=='same-request recovery permits input'
        assert len([w for w in writes if w[0]=='/api/v1/sessions/'+same_session+'/messages'])==1
        assert len([w for w in writes if w[0]=='/api/v1/sessions'])==same_session_posts
        assert Path(info['workspace'],'mcp-marker.txt.launch').read_text()==same_launches
        # Recover a different, genuinely created request after apply. The proxy exposes
        # its owned starting state once, then the actual durable completed claim.
        second_request='pty-second-recovery'
        external=json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1/sessions',data=json.dumps({'mode':'chat_only','request_id':second_request}).encode(),headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'Content-Type':'application/json','X-Agent-Protocol':'personal-agent-rebuild-v1'})))
        state['admission_override_path']='/api/v1/admissions/'+second_request
        state['admission_override']={'request_id':second_request,'session_id':external['id'],'state':'starting','mcp_opt_in':False,'reason':''}
        output=b'';send('/new recover '+second_request);wait_for('starting: admission '+second_request+' / session '+external['id'])
        output=b'';os.write(master,b'r');wait_for('Admission recovered read-only');assert len(writes)==before_posts+2
        # Actual machine admission response loss, with explicit MCP and initial
        # content: committed empty session is recovered, prompt is never replayed.
        db.execute("UPDATE project_catalog_lifecycle SET state='active',revision=revision+1 WHERE project_id='metadata'");db.commit()
        args=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',proxy_url]
        state['mode']='lost';before=len(get('/sessions'))
        result=subprocess.run(args+['--mode','default','--mcp','--project-label','metadata','--print','private initial','--format','json'],env=env,capture_output=True,text=True,timeout=20)
        assert result.returncode==4,(result.stdout,result.stderr);value=json.loads(result.stdout);assert value['status']=='admission_recovered';assert value['request_id'];assert value['session_id'];assert 'private initial' not in result.stdout
        assert len(get('/sessions'))==before+1;assert get('/sessions/'+value['session_id']+'/snapshot')['runs']==[];assert len(Path(info['workspace'],'mcp-marker.txt.launch').read_text().splitlines())==2
        post_count=len(writes)
        recovered=subprocess.run(args+['--admission',value['request_id'],'--format','json'],env=env,capture_output=True,text=True,timeout=15);assert recovered.returncode==0,recovered.stderr;assert json.loads(recovered.stdout)['session']['project_label']['name']=='Changed catalog';assert len(writes)==post_count
        for forbidden in [['--session',parent,'--project-label','metadata','--print','no'],['--session',parent,'--manage','projects','--project-label','metadata']]:
            r=subprocess.run(args+forbidden,env=env,capture_output=True,text=True,timeout=15);assert r.returncode==2;assert len(writes)==post_count
        clean=subprocess.run(args+['--project-label','metadata','--new-session','--format','json'],env=env,capture_output=True,text=True,timeout=15);assert clean.returncode==0,clean.stderr;assert json.loads(clean.stdout)['session']['project_label']['project_id']=='metadata'
        state['drop_message']=True
        once=subprocess.run(args+['--project-label','metadata','--print','once after lost message acknowledgement','--format','json'],env=env,capture_output=True,text=True,timeout=20);assert once.returncode==0,(once.stdout,once.stderr);once_result=json.loads(once.stdout);once_snapshot=get('/sessions/'+once_result['session_id']+'/snapshot');assert once_snapshot['session']['project_label']['project_id']=='metadata';assert len(once_snapshot['runs'])==1;assert once_snapshot['runs'][0]['input']=='once after lost message acknowledgement';assert len([w for w in writes if w[0]=='/api/v1/sessions/'+once_result['session_id']+'/messages'])==1
        db.close();send('/exit');exit_proc();os.close(slave)
        print('PASS real Ink+CLI label selection/clear, lost admission GET recovery, repeated y quarantine, one MCP launch per explicit admission, no initial-content replay, branch/Job historical label after rename/archive and pure JSON recovery')
    finally:
        release.set();Path('/tmp/task7e6b-admission-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if proxy:proxy.shutdown();proxy.server_close()
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
