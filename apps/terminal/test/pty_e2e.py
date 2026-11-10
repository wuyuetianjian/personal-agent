import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, urllib.request, uuid
from pathlib import Path
with tempfile.TemporaryDirectory(prefix='agent-pty-') as td:
    binary=td+'/fixture'
    subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True)
    env={**os.environ,'PA_AGENT_TOKEN':'pty-test'}
    server=subprocess.Popen([binary],env=env,stdout=subprocess.PIPE,text=True)
    try:
        info=json.loads(server.stdout.readline()); master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',40,100,0,0))
        proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode','default'],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
        output=b''
        def wait_for(term, seconds=15):
            global output
            deadline=time.monotonic()+seconds
            while time.monotonic()<deadline:
                if term.encode() in output:return
                ready,_,_=select.select([master],[],[],.2)
                if ready:
                    try: output+=os.read(master,65536)
                    except OSError: break
            raise AssertionError(f'missing {term!r} in {output[-5000:]!r}')
        wait_for('default');time.sleep(.3);os.write(master,b'edit');time.sleep(.3);os.write(master,b'\r');wait_for('批准 y');os.write(master,b'y');wait_for('工具结果已收到');assert Path(info['workspace'],'note.txt').read_text()=='new 你好\n'
        output=b'';os.write(master,b'wait');time.sleep(.3);os.write(master,b'\r');wait_for('model');os.write(master,b'\x03');wait_for('cancelled')
        output=b'';os.write(master,'你好'.encode());time.sleep(.3);os.write(master,b'\r');wait_for('你好，');assert '真实流式测试完成。'.encode() not in output;wait_for('真实流式测试完成。')
        def api(path, body=None):
            data=None if body is None else json.dumps(body).encode();req=urllib.request.Request(info['url']+'/api/v1'+path,data=data,headers={'Authorization':'Bearer pty-test','X-Agent-Protocol':'personal-agent-rebuild-v1','Content-Type':'application/json'});return json.load(urllib.request.urlopen(req))
        sid=api('/sessions',{'request_id':str(uuid.uuid4()),'mode':'chat_only'})['id']
        for i in range(51):
            ack=api('/sessions/'+sid+'/messages',{'request_id':str(uuid.uuid4()),'content':f'history-seed-{i:02d}'})
            for _ in range(200):
                snap=api('/sessions/'+sid+'/snapshot')
                if any(r['id']==ack['run_id'] and r['state']=='completed' for r in snap['runs']):break
                time.sleep(.01)
            else:raise AssertionError('seed did not complete')
        output=b'';os.write(master,('/resume '+sid).encode());time.sleep(.3);os.write(master,b'\r');wait_for('history-seed-50');assert b'history-seed-00' not in output
        output=b'';os.write(master,b'/history older');time.sleep(.3);os.write(master,b'\r');wait_for('history-seed-00');assert b'history-seed-50' in output
        os.write(master,b'/exit');time.sleep(.3);os.write(master,b'\r')
        # A real terminal consumes repaint output during shutdown. Waiting on the
        # child without reading the PTY deadlocks when a long history fills it.
        deadline=time.monotonic()+10;shutdown_bytes=0
        while proc.poll() is None and time.monotonic()<deadline:
            if select.select([master],[],[],.1)[0]:
                try:data=os.read(master,65536)
                except OSError:break
                output+=data;shutdown_bytes+=len(data)
        proc.wait(timeout=.1);assert proc.returncode==0,proc.returncode
        print('Shutdown PTY bytes drained:',shutdown_bytes)
        Path('/tmp/task6b-pty-output.log').write_bytes(output)
        print('PASS: real PTY edit → approval → file mutation → answer → Ctrl+C cancel → Chinese incremental stream → switch session and older history → exit')
    finally:
        if 'proc' in locals() and proc.poll() is None:proc.kill();proc.wait()
        server.send_signal(signal.SIGINT);server.wait(timeout=10)
