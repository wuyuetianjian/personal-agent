"""Actual Ink/machine + owned Go fixture, real forwarded lost acknowledgments."""
import http.client
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, urllib.error, threading, socket
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
MANIFEST='''id: ink.read
version: "1"
name: Owned read/search
description: inert
status: active
inputs: {readme: {type: object}, search: {type: object}}
requires: {capabilities: [workspace.read, workspace.search]}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 2, max_tool_calls: 2}
verification: {min_coverage: 0, allow_conflicts: false}
workflow: {nodes: [{id: readme, capability: workspace.read}, {id: search, capability: workspace.search, depends_on: [readme]}]}
'''
def exercise(recovery=False):
    with tempfile.TemporaryDirectory(prefix='hooks-pty-') as td:
        binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
        subprocess.run(['node','apps/terminal/test/build_hooks_cli.mjs',td+'/cli.mjs'],check=True,timeout=30);Path(td+'/node_modules').symlink_to(Path('apps/terminal/node_modules').resolve(),target_is_directory=True)
        env={**os.environ,'PA_AGENT_TOKEN':'owned-skill-run','XDG_STATE_HOME':td+'/state'}
        server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        proc=None;master=None;slave=None;proxy=None;output=b'';evidence=b'';writes=[];state={'drop':'','hold_terminal':False,'refuse_control':False}
        try:
            assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
            def api(method,path,value=None):
                req=urllib.request.Request(info['url']+'/api/v1'+path,data=json.dumps(value).encode() if value is not None else None,method=method,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'})
                try:return json.load(urllib.request.urlopen(req,timeout=15))
                except urllib.error.HTTPError as e:raise AssertionError((path,e.code,e.read().decode())) from e
            sid=api('POST','/sessions',{'request_id':'session','mode':'default' if recovery else 'plan'})['id']
            p=api('POST','/sessions/'+sid+'/skills/preview',{'request_id':'import','kind':'import','manifest_text':MANIFEST,'catalog_id':'','revision_id':''})
            d=api('POST','/sessions/'+sid+'/skills/decision',{'request_id':'import-d','operation_id':p['operation_id'],'digest':p['digest'],'decision':'confirm'})
            p=api('POST','/sessions/'+sid+'/skills/preview',{'request_id':'enable','kind':'enable','manifest_text':'','catalog_id':d['catalog_id'],'revision_id':d['revision_id']})
            api('POST','/sessions/'+sid+'/skills/decision',{'request_id':'enable-d','operation_id':p['operation_id'],'digest':p['digest'],'decision':'confirm'})
            inputs=td+'/literal inputs.json';Path(inputs).write_text(json.dumps({'readme':{'path':'note.txt'},'search':{'paths':['note.txt'],'query':'fixture'}}))
            class Proxy(BaseHTTPRequestHandler):
                def log_message(self,*args):pass
                def do_GET(self):self.forward()
                def do_POST(self):self.forward()
                def forward(self):
                    body=self.rfile.read(int(self.headers.get('Content-Length','0'))) if self.command=='POST' else None
                    if self.command=='POST' and '/runs/' in self.path and state['refuse_control']:
                        state['refuse_control']=False;payload=json.dumps({'error':{'code':'service_capacity'}}).encode();self.send_response(503);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(payload)));self.end_headers();self.wfile.write(payload);return
                    req=urllib.request.Request(info['url']+self.path,data=body,method=self.command,headers={k:v for k,v in self.headers.items() if k.lower() not in ['host','connection','content-length']})
                    try:response=urllib.request.urlopen(req,timeout=20)
                    except urllib.error.HTTPError as e:response=e
                    try:
                        if body is not None:writes.append((self.path,json.loads(body)))
                        if self.command=='POST' and state['drop'] and self.path.endswith(state['drop']):
                            response.read();state['drop']='';self.close_connection=True;self.connection.shutdown(socket.SHUT_RDWR);return
                        self.send_response(response.status)
                        for key in ['Content-Type','Content-Length']:
                            if response.headers.get(key):self.send_header(key,response.headers[key])
                        self.end_headers()
                        held=False;buffer=b''
                        while True:
                            chunk=response.read1(65536)
                            if not chunk:break
                            if '/events?' in self.path and state['hold_terminal']:
                                buffer+=chunk
                                while b'\n\n' in buffer:
                                    frame,buffer=buffer.split(b'\n\n',1)
                                    if any(('\"type\":\"run.'+kind+'\"').encode() in frame for kind in ['completed','failed','cancelled','needs_reconciliation']):held=True
                                    if not held:self.wfile.write(frame+b'\n\n');self.wfile.flush()
                            else:self.wfile.write(chunk);self.wfile.flush()
                    except (BrokenPipeError,ConnectionResetError,TimeoutError,http.client.IncompleteRead):pass
                    finally:response.close()
            proxy=ThreadingHTTPServer(('127.0.0.1',0),Proxy);proxy.daemon_threads=True;threading.Thread(target=proxy.serve_forever,daemon=True).start();url='http://127.0.0.1:'+str(proxy.server_port)
            def start():
                nonlocal proc,master,slave,output
                master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0));output=b''
                proc=subprocess.Popen(['node',td+'/cli.mjs','--url',url,'--session',sid],stdin=slave,stdout=slave,stderr=slave,env=env)
            def wait_for(term,seconds=15):
                nonlocal output,evidence
                until=time.monotonic()+seconds
                while time.monotonic()<until:
                    if term.encode() in output:return
                    if select.select([master],[],[],.1)[0]:
                        data=os.read(master,65536);output+=data;evidence+=data
                Path('/tmp/hooks-pty-debug.log').write_bytes(evidence);raise AssertionError('missing '+term+': '+repr(output[-1200:]))
            def send(value):
                nonlocal output
                output=b'';os.write(master,value.encode());time.sleep(.2);os.write(master,b'\r')
            def kill():
                proc.kill();proc.wait(timeout=5);os.close(master);os.close(slave)
            policy=td+'/policy.json';Path(policy).write_text(json.dumps({'schema_version':1,'evaluator':'declarative-hooks-v1','rules':[{'id':'deny-search','event':'before_node','node_ids':[],'capabilities':['workspace.search'],'statuses':[],'effect':'deny'}]}))
            if recovery:
                # A separate session's native edit approval remains untouched while Hook IDs recover.
                other=sid
                api('POST','/sessions/'+other+'/messages',{'request_id':'edit','content':'edit'})
            start();wait_for('Enter 发送');send('/hooks import-file '+policy);wait_for('Declarative Hooks: pending')
            before=len(writes);os.write(master,b'\r');time.sleep(.3);assert len(writes)==before,'Enter confirmed Hook preview'
            output=b'';os.write(master,b'y');wait_for('imported');assert api('GET','/sessions/'+sid+'/hooks')['state']=='disabled'
            output=b'';os.write(master,b'\r');wait_for('Hook result dismissed')
            revision=api('GET','/sessions/'+sid+'/hooks/revisions')['revisions'][0]['revision_id']
            send('/hooks enable '+revision);wait_for('Declarative Hooks: pending')
            if recovery:state['drop']='/hooks/decision'
            output=b'';os.write(master,b'y');wait_for('unknown:' if recovery else 'enabled')
            if recovery:
                count=len(writes);saved=list(Path(td+'/state/personal-agent').glob('hooks-*.json'));assert len(saved)==1
                ref=json.loads(saved[0].read_text());assert set(ref)=={'session_id','request_id','operation_id'}
                assert 'deny-search' not in saved[0].read_text()
                os.write(master,b'yyy\r');time.sleep(.2);assert len(writes)==count
                kill();start();wait_for('unknown:');output=b'';os.write(master,b'r');wait_for('enabled');assert len(writes)==count
                assert api('GET','/sessions/'+other+'/snapshot')['runs'][0]['phase']=='waiting_approval'
            assert api('GET','/sessions/'+sid+'/hooks')['generation']==1
            output=b'';os.write(master,b'\r');wait_for('Hook result dismissed')
            if recovery:
                os.write(master,b'\r\r');time.sleep(.2)
                approval_run=api('GET','/sessions/'+sid+'/snapshot')['runs'][0]
                assert approval_run['phase']=='waiting_approval'
                api('POST','/runs/'+approval_run['id']+'/cancel',{'request_id':'explicit-owned-cancel'})
                until=time.monotonic()+5
                while api('GET','/sessions/'+sid+'/snapshot')['runs'][0]['state']=='running' and time.monotonic()<until:time.sleep(.05)
            send('/skill-run preview '+d['catalog_id']+' '+d['revision_id']+' '+inputs);wait_for('deny-search');output=b'';os.write(master,b'y');wait_for('admitted')
            until=time.monotonic()+5;run=api('GET','/sessions/'+sid+'/skill-runs')['runs'][0]
            while run['execution']=='running' and time.monotonic()<until:time.sleep(.05);run=api('GET','/sessions/'+sid+'/skill-runs/'+run['run_id'])
            assert run['schema_version']==2 and run['nodes'][0]['outcome']=='success' and run['nodes'][1]['outcome']=='not_started' and run['nodes'][1]['result'] is None
            output=b'';os.write(master,b'\r');wait_for('Skill run result dismissed');send('/hooks receipts '+run['run_id']);wait_for('hook_denied' if False else 'deny-search')
            receipts=api('GET','/sessions/'+sid+'/skill-runs/'+run['run_id']+'/hooks');assert len(receipts['receipts'])==4
            import sqlite3
            db=next(Path(info['runtime_parent']).rglob('*.db'));conn=sqlite3.connect(db)
            assert conn.execute('SELECT count(*) FROM side_effect_records WHERE workflow_id=?',(run['run_id'],)).fetchone()[0]==1
            if recovery:
                def machine(args):
                    r=subprocess.run(['node',td+'/cli.mjs','--url',url,'--session',sid,'--format','json']+args,env=env,capture_output=True,text=True,timeout=15)
                    value=json.loads(r.stdout);assert 'policy_text' not in r.stdout or value.get('state')=='pending'
                    return r.returncode,value
                status,preview=machine(['--hooks','disable','--request-id','machine-p']);assert status==0
                state['drop']='/hooks/decision';status,lost=machine(['--hooks','cancel','--operation-id',preview['operation_id'],'--digest',preview['digest'],'--request-id','machine-d']);assert status==2 and lost['state']=='unknown'
                count=len(writes);recovered=subprocess.run(['node',td+'/cli.mjs']+lost['recovery_args'],env=env,capture_output=True,text=True,timeout=15)
                assert recovered.returncode==0 and json.loads(recovered.stdout)['state']=='cancelled' and len(writes)==count
                assert api('GET','/sessions/'+sid+'/hooks')['generation']==1
            assert conn.execute('SELECT count(*) FROM interaction_hook_audits WHERE session_id=?',(sid,)).fetchone()[0]==(3 if recovery else 2)
            for _ in range(3):assert api('GET','/sessions/'+sid+'/skill-runs/'+run['run_id']+'/hooks')==receipts
            conn.close();Path('.superpowers/sdd/declarative-hooks/t2-hooks-'+('recovery' if recovery else 'basic')+'-pty.log').write_bytes(evidence)
            print('Hook Ink lifecycle, pinned deny, ID-only recovery and durable counts passed')
        finally:
            if proc and proc.poll() is None:proc.kill();proc.wait(timeout=5)
            if master is not None:
                try:os.close(master);os.close(slave)
                except OSError:pass
            if proxy:proxy.shutdown();proxy.server_close()
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=5)
            except subprocess.TimeoutExpired:server.kill();server.wait()
