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
with tempfile.TemporaryDirectory(prefix='skill-run-pty-') as td:
    binary=td+'/fixture';subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'owned-skill-run','XDG_STATE_HOME':td+'/state'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;slave=None;proxy=None;output=b'';evidence=b'';writes=[];state={'drop':'','hold_terminal':False,'refuse_control':False}
    try:
        assert select.select([server.stdout],[],[],10)[0];info=json.loads(server.stdout.readline())
        def api(method,path,value=None):
            req=urllib.request.Request(info['url']+'/api/v1'+path,data=json.dumps(value).encode() if value is not None else None,method=method,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'})
            try:return json.load(urllib.request.urlopen(req,timeout=15))
            except urllib.error.HTTPError as e:raise AssertionError((path,e.code,e.read().decode())) from e
        sid=api('POST','/sessions',{'request_id':'session','mode':'plan'})['id']
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
            global proc,master,slave,output
            master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',150,180,0,0));output=b''
            proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',url,'--session',sid],stdin=slave,stdout=slave,stderr=slave,env=env)
        def wait_for(term,seconds=15):
            global output,evidence
            until=time.monotonic()+seconds
            while time.monotonic()<until:
                if term.encode() in output:return
                if select.select([master],[],[],.1)[0]:
                    data=os.read(master,65536);output+=data;evidence+=data
            Path('/tmp/skill-run-pty-debug.log').write_bytes(evidence);raise AssertionError('missing '+term+': '+repr(output[-1200:]))
        def send(value):
            global output
            output=b'';os.write(master,value.encode());time.sleep(.2);os.write(master,b'\r')
        def kill():
            proc.kill();proc.wait(timeout=5);os.close(master);os.close(slave)
        start();wait_for('Enter 发送');state['drop']='/skill-runs/preview';send('/skill-run preview '+d['catalog_id']+' '+d['revision_id']+' '+inputs);wait_for('unknown:')
        count=len(writes);os.write(master,b'yyy');time.sleep(.2);assert len(writes)==count
        kill();start();wait_for('unknown:');assert len(api('GET','/sessions'))==1;output=b'';os.write(master,b'r');wait_for('Skill run admission: pending');assert len(writes)==count
        state['drop']='/skill-runs/decision';output=b'';os.write(master,b'y');wait_for('unknown:');count=len(writes);kill();start();wait_for('unknown:');output=b'';os.write(master,b'r');wait_for('admitted');assert len(writes)==count
        run=api('GET','/sessions/'+sid+'/skill-runs')['runs'][0]
        until=time.monotonic()+5
        while run['execution']!='completed' and time.monotonic()<until:time.sleep(.05);run=api('GET','/sessions/'+sid+'/skill-runs/'+run['run_id'])
        assert run['execution']=='completed' and [n['outcome'] for n in run['nodes']]==['success','success'];assert run['evaluation']=='not_run'
        output=b'';os.write(master,b'\r');wait_for('Skill run result dismissed');send('/skill-run show '+run['run_id']);wait_for('completed');wait_for('not_evaluated')
        # F1: completed deterministic Skill followed by ordinary same-session chat.
        send('ordinary chat after completed Skill');wait_for('真实流式测试完成')
        until=time.monotonic()+5
        while api('GET','/sessions/'+sid+'/snapshot')['runs'][-1]['state']!='completed' and time.monotonic()<until:time.sleep(.05)
        snap=api('GET','/sessions/'+sid+'/snapshot');assert snap['runs'][-1]['state']=='completed' and len(snap['runs'])==2
        view=api('GET','/sessions/'+sid+'/context-view');assert any(m['role']=='user' and m['content']=='ordinary chat after completed Skill' for m in view['original']);assert all(m['content']!='Explicit read-only skill run' for m in view['original'])
        assert len(api('GET','/sessions'))==1 and api('GET','/sessions/'+sid+'/skill-runs/'+run['run_id'])==run
        state['drop']='/cancel';send('/skill-run stop '+run['run_id']);wait_for('unknown:');count=len(writes);kill();start();wait_for('unknown:');output=b'';os.write(master,b'r');wait_for('Skill run execution: completed');assert len(writes)==count
        saved=list(Path(td+'/state/personal-agent').glob('skill-run-*.json'));assert not saved,'resolved receipt should clear quarantine IDs'
        base=['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',url,'--session',sid,'--format','json']
        def cli(args):return subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=20)
        result=cli(['--skill-run','list']);assert result.returncode==0,result.stderr;assert len(json.loads(result.stdout)['runs'])==1
        state['drop']='/skill-runs/preview';result=cli(['--skill-run','preview','--catalog',d['catalog_id'],'--revision',d['revision_id'],'--inputs-file',inputs,'--request-id','machine-preview']);assert result.returncode==2,result.stderr;unknown=json.loads(result.stdout);assert unknown['recovery_args'];count=len(writes)
        result=cli(['--skill-run','lookup','--request-id','machine-preview']);assert result.returncode==0,result.stderr;p=json.loads(result.stdout);assert p['state']=='pending';assert len(writes)==count
        result=cli(['--skill-run','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','machine-confirm']);assert result.returncode==0,result.stderr;d2=json.loads(result.stdout);assert d2['state']=='admitted'
        Path(inputs).write_text(json.dumps({'readme':{'path':'missing.txt'},'search':{'paths':['note.txt'],'query':'fixture'}}));time.sleep(.2)
        result=cli(['--skill-run','preview','--catalog',d['catalog_id'],'--revision',d['revision_id'],'--inputs-file',inputs,'--request-id','failure-preview']);assert result.returncode==0,result.stderr;p=json.loads(result.stdout)
        result=cli(['--skill-run','confirm','--operation-id',p['operation_id'],'--digest',p['digest'],'--request-id','failure-confirm']);assert result.returncode==0,result.stderr;r=json.loads(result.stdout);time.sleep(.2)
        result=cli(['--skill-run','show','--run-id',r['run_id']]);assert result.returncode==0,result.stderr;failed=json.loads(result.stdout);assert failed['execution']=='failed' and failed['nodes'][0]['outcome']=='failed' and failed['nodes'][1]['outcome']=='not_started'
        # Review R1/R4: real absent/foreign/model targets cannot mutate first;
        # one explicit pre-reservation transport capacity refusal is dismissible.
        output=b'';os.write(master,b'\r');wait_for('Skill run result dismissed')
        foreign_owner=api('POST','/sessions',{'request_id':'foreign-owner','mode':'default'})['id']
        other_p=api('POST','/sessions/'+foreign_owner+'/skill-runs/preview',{'request_id':'foreign-run-p','catalog_id':d['catalog_id'],'revision_id':d['revision_id'],'execution_profile':'readonly-workspace-v1','inputs':{'readme':{'path':'note.txt'},'search':{'paths':['note.txt'],'query':'old'}}})
        other_r=api('POST','/sessions/'+foreign_owner+'/skill-runs/decision',{'request_id':'foreign-run-d','operation_id':other_p['operation_id'],'digest':other_p['digest'],'decision':'confirm'})
        until=time.monotonic()+5
        while api('GET','/sessions/'+foreign_owner+'/skill-runs/'+other_r['run_id'])['execution'] in ['queued','running'] and time.monotonic()<until:time.sleep(.05)
        model_owner=api('POST','/sessions',{'request_id':'ordinary-model-owner','mode':'default'})['id']
        model=api('POST','/sessions/'+model_owner+'/messages',{'request_id':'foreign-model','content':'wait'})['run_id']
        for target in ['absent-run',other_r['run_id'],model]:
            before=len(writes);send('/skill-run stop '+target);wait_for('Skill control refused');assert len(writes)==before,'owner/type rejection posted a control';output=b'';os.write(master,b'\r');wait_for('Skill run result dismissed')
        api('POST','/runs/'+model+'/cancel',{'request_id':'cleanup-model'})
        state['refuse_control']=True;send('/skill-run stop '+run['run_id']);wait_for('service_capacity');wait_for('Skill control refused');output=b'';os.write(master,b'\r');wait_for('Skill run result dismissed')
        # Review R3: withhold real terminal event frames, never invent a run kind.
        # Each path gets an isolated recovery file. Canonical Skill reads and
        # control requests are always forwarded to the actual owned service.
        kill()
        for entry in ['escape','ctrl-c','cancel','resume']:
            env['XDG_STATE_HOME']=td+'/generic-'+entry;state['hold_terminal']=True;start();wait_for('Enter 发送')
            gp=api('POST','/sessions/'+sid+'/skill-runs/preview',{'request_id':'generic-'+entry+'-p','catalog_id':d['catalog_id'],'revision_id':d['revision_id'],'execution_profile':'readonly-workspace-v1','inputs':{'readme':{'path':'note.txt'},'search':{'paths':['note.txt'],'query':'old'}}})
            gr=api('POST','/sessions/'+sid+'/skill-runs/decision',{'request_id':'generic-'+entry+'-d','operation_id':gp['operation_id'],'digest':gp['digest'],'decision':'confirm'})
            wait_for(gr['run_id']);action='resume' if entry=='resume' else 'cancel';state['drop']='/'+action;before=len(writes);output=b''
            if entry=='escape':os.write(master,b'\x1b')
            elif entry=='ctrl-c':os.write(master,b'\x03')
            else:send('/'+entry)
            wait_for('unknown:');assert len(writes)==before+1
            os.write(master,b'\x1b\x03');time.sleep(.2);assert len(writes)==before+1
            recovery=list(Path(env['XDG_STATE_HOME']+'/personal-agent').glob('skill-run-*.json'));assert len(recovery)==1;ref=json.loads(recovery[0].read_text());assert ref['session_id']==sid and ref['run_id']==gr['run_id'] and ref['action']==action
            kill();state['hold_terminal']=False;start();wait_for('unknown:');output=b'';os.write(master,b'r')
            wait_for('needs_reconciliation' if entry=='resume' else 'Skill run execution: completed');assert len(writes)==before+1
            if entry=='resume':assert recovery[0].exists(),'unacknowledged reserved resume must stay quarantined'
            kill()
        proc=None;master=None;slave=None
        print('Skill runs actual Ink/machine: read/search then same-session chat/context, lost preview/decision/stop across restart, original GET only, ID-only quarantine, repeated keys, failed partial evidence, pure JSON stdout')
    finally:
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=5)
        if master is not None:
            try:os.close(master)
            except OSError:pass
        if slave is not None:
            try:os.close(slave)
            except OSError:pass
        if proxy:proxy.shutdown();proxy.server_close()
        server.send_signal(signal.SIGINT);server.wait(timeout=10)
