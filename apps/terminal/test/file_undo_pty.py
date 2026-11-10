"""Actual Ink + authenticated Go API, native inverse effect in an owned temp file."""
import urllib.request
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, uuid
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='undo-pty-') as td:
    binary=td+'/fixture'
    subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'undo-pty-local'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;output=b'';evidence=b''
    try:
        assert select.select([server.stdout],[],[],10)[0],'fixture startup timeout'
        info=json.loads(server.stdout.readline());target=Path(info['workspace'],'note.txt')
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','acceptEdits'],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
        def wait_for(term,seconds=15):
            global output,evidence
            until=time.monotonic()+seconds
            while time.monotonic()<until:
                if term.encode() in output:return
                if select.select([master],[],[],.1)[0]:
                    try:data=os.read(master,65536)
                    except OSError:break
                    output+=data;evidence+=data
            raise AssertionError(f'missing {term!r}: {output[-9000:]!r}')
        def send(value):
            os.write(master,value.encode());time.sleep(.15);os.write(master,b'\r')
        def api(path,body=None):
            data=None if body is None else json.dumps(body).encode()
            req=urllib.request.Request(info['url']+'/api/v1'+path,data=data,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN'],'X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'})
            return json.load(urllib.request.urlopen(req))
        wait_for('acceptEdits');send('edit');wait_for('工具结果已收到');assert target.read_text()=='new 你好\n'
        source=api('/sessions')[0]['id'];inv=api('/sessions/'+source+'/edit-checkpoints');checkpoint=inv['checkpoints'][0];assert checkpoint['disposition']=='available'
        output=b'';send('/rewind files');wait_for('checkpoints');assert target.read_text()=='new 你好\n';assert len(api('/sessions/'+source+'/snapshot')['runs'])==1
        output=b'';send('/rewind files '+checkpoint['id']);wait_for('Explicit native file undo');wait_for('批准 y');wait_for('Fresh approval always required');assert target.read_text()=='new 你好\n';snap=api('/sessions/'+source+'/snapshot');undo=snap['runs'][-1];assert undo['approval']['id']!=checkpoint['id'];assert undo['text']==''
        output=b'';os.write(master,b'y');wait_for('completed');assert target.read_text()=='old\n';inv=api('/sessions/'+source+'/edit-checkpoints');assert inv['checkpoints'][0]['disposition']=='undone';assert len(inv['checkpoints'])==1
        # Same decision, fresh request ID, cannot execute again.
        try:api('/approvals/'+undo['approval']['id']+'/decision',{'request_id':str(uuid.uuid4()),'digest':undo['approval']['digest'],'decision':'approved'});raise AssertionError('approval replay accepted')
        except urllib.error.HTTPError as e:assert e.code==409
        assert target.read_text()=='old\n';assert len(api('/sessions/'+source+'/snapshot')['runs'])==2
        output=b'';send('/rewind files '+checkpoint['id']);wait_for('context_operation_rejected');assert len(api('/sessions/'+source+'/snapshot')['runs'])==2
        output=b'';send('/compact view');wait_for('\"selection\": \"original\"');assert target.read_text()=='old\n'
        send('/exit');until=time.monotonic()+10
        while proc.poll() is None and time.monotonic()<until:
            if select.select([master],[],[],.1)[0]:
                try:evidence+=os.read(master,65536)
                except OSError:break
        proc.wait(timeout=2);assert proc.returncode==0
        print('PASS real native undo PTY: acceptEdits still asks, exact current/restored bytes, provenance, one inverse effect, no replay, context intact')
    finally:
        Path('/tmp/task7c2-file-undo-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
