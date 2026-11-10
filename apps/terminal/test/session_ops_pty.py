"""Actual Ink + authenticated Go API + owned stdio helper, no external services."""
import urllib.request
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, re
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='session-pty-') as td:
    binary=td+'/fixture'
    subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    env={**os.environ,'PA_AGENT_TOKEN':'mcp-pty-local'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    proc=None;master=None;output=b'';evidence=b''
    try:
        assert select.select([server.stdout],[],[],10)[0],'fixture startup timeout'
        info=json.loads(server.stdout.readline());marker=Path(info['workspace'],'mcp-marker.txt')
        assert not Path(str(marker)+'.launch').exists()
        master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
        proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','chat_only'],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
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
        def get(path):
            req=urllib.request.Request(info['url']+'/api/v1'+path,headers={'Authorization':'Bearer '+env['PA_AGENT_TOKEN']})
            return json.load(urllib.request.urlopen(req))
        wait_for('chat_only');send('Never transmit');wait_for('completed')
        source=get('/sessions')[0]['id'];original=get('/sessions/'+source+'/snapshot')
        output=b'';send('/branch latest');wait_for('parent '+source);wait_for('point')
        child=get('/sessions')[0]['id'];assert child!=source
        output=b'';send('/compact');wait_for('Compacted: local-omit-resolved-v1')
        view=get('/sessions/'+child+'/context-view');assert view['selection']=='compacted';assert view['original'][0]['content']=='Never transmit'
        output=b'';send('/compact original');wait_for('Original context selection restored');assert get('/sessions/'+child+'/context-view')['selection']=='original'
        output=b'';send('/rewind files');wait_for('not conversation rewind');assert len(get('/sessions'))==2
        output=b'';send('/clear');wait_for('clear confirm');assert len(get('/sessions'))==2
        output=b'';send('/clear confirm');wait_for('New empty conversation context');empty=get('/sessions')[0]['id'];assert get('/sessions/'+empty+'/context-view')['original']==[]
        assert get('/sessions/'+source+'/snapshot')==original
        assert Path(info['workspace'],'note.txt').read_text()=='old\n'
        output=b'';send('/resume '+source);wait_for('已切换会话');wait_for('Never transmit')
        send('/exit')
        until=time.monotonic()+10
        while proc.poll() is None and time.monotonic()<until:
            if select.select([master],[],[],.1)[0]:
                try:evidence+=os.read(master,65536)
                except OSError:break
        proc.wait(timeout=2);assert proc.returncode==0
        print('PASS real session PTY: branch, compaction, original selection, read-only native checkpoint discovery, clear consent and read-only reopen')
    finally:
        Path('/tmp/task7c-session-pty.log').write_bytes(evidence)
        if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
        if master is not None:os.close(master)
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            try:server.wait(timeout=10)
            except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
