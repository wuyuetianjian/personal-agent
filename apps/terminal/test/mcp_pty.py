"""Actual Ink + authenticated Go API + owned stdio helper, no external services."""
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, re, urllib.request
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='mcp-pty-') as td:
    binary=td+'/fixture'
    subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'mcp-pty-local'}
    server=subprocess.Popen([binary,'--safe-mcp'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;output=b'';evidence=b''
    try:
        assert select.select([server.stdout],[],[],10)[0],'fixture startup timeout'
        info=json.loads(server.stdout.readline());marker=Path(info['workspace'],'mcp-marker.txt')
        assert not Path(str(marker)+'.launch').exists()
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','acceptEdits','--mcp'],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
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
        def get(path):return json.load(urllib.request.urlopen(urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})))
        def current_approval(previous=''):
            until=time.monotonic()+15
            while time.monotonic()<until:
                sessions=get('/sessions')
                if sessions:
                    runs=get('/sessions/'+sessions[0]['id']+'/snapshot')['runs']
                    if runs and runs[-1]['id']!=previous and runs[-1].get('approval',{}).get('decision')=='pending':
                        run=runs[-1];wait_for(run['id']);wait_for(run['approval']['expires_at']);wait_for('批准 y');return run
                if select.select([master],[],[],.05)[0]:
                    global output,evidence
                    data=os.read(master,65536);output+=data;evidence+=data
            raise AssertionError('fresh exact approval not displayed')
        wait_for('acceptEdits');wait_for('discovered');send('/mcp');wait_for('HTTP, Tasks');assert not marker.exists()
        output=b'';send('mcp');first=current_approval()
        clean=re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]',b'',output).decode(errors='replace')
        for value in ['owned-fixture','"tool": "write"','schema_digest','config_fingerprint','generation','NON-SANDBOXED','current OS user','no inherited','hello 世界','unknown/writable']:
            assert value in clean,(value,clean[-8000:])
        assert not marker.exists();output=b'';os.write(master,b'n');wait_for('failed');assert not marker.exists()
        output=b'';send('mcp');current_approval(first['id']);output=b'';os.write(master,b'y')
        # A 10 ms sleep can coalesce two PTY writes into literal 'yy', correctly
        # treated as draft text. Observe the first key's effect before the next.
        until=time.monotonic()+15
        while time.monotonic()<until:
            if marker.exists() and marker.read_text().strip():break
            if select.select([master],[],[],.05)[0]:
                data=os.read(master,65536);output+=data;evidence+=data
        assert marker.exists() and marker.read_text().strip(),'first approval key not handled'
        os.write(master,b'y');wait_for('工具结果已收到');wait_for('completed');wait_for('known_response');wait_for('owned');wait_for('result"')
        rows=marker.read_text().splitlines();assert len(rows)==1;assert json.loads(rows[0])['arguments']['value']=='hello 世界'
        time.sleep(.2);assert len(marker.read_text().splitlines())==1
        os.write(master,b'\x03')
        until=time.monotonic()+10
        while proc.poll() is None and time.monotonic()<until:
            if select.select([master],[],[],.1)[0]:
                try:evidence+=os.read(master,65536)
                except OSError:break
        proc.wait(timeout=2);assert proc.returncode==0
        print('PASS real MCP PTY: explicit launch, read-only status, exact review, denial and one effect')
    finally:
        Path('/tmp/task7b-mcp-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
