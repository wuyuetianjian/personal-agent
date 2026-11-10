"""Real Ink/Go acceptance. Only fixed exec in fixture-owned temporary directories."""
import os, pty, subprocess, tempfile, json, select, time, fcntl, termios, struct, signal, re
from pathlib import Path

with tempfile.TemporaryDirectory(prefix='agent-acceptance-pty-') as td:
    binary=td+'/fixture'
    subprocess.run(['go','build','-o',binary,'./cmd/agent-fixture'],check=True,timeout=60)
    for args,mode in [(['--safe-exec'],'acceptEdits'),(['--chat-only-fixture'],'chat_only')]:
        env={**os.environ,'PA_AGENT_TOKEN':'pty-acceptance'}
        server=subprocess.Popen([binary,*args],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        proc=None;master=None;output=b'';evidence=b''
        try:
            ready,_,_=select.select([server.stdout],[],[],10)
            assert ready,'fixture startup timeout'
            info=json.loads(server.stdout.readline())
            master,slave=pty.openpty();fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',100,180,0,0))
            proc=subprocess.Popen(['node','--import','./apps/terminal/node_modules/tsx/dist/loader.mjs','apps/terminal/src/cli.tsx','--url',info['url'],'--mode',mode],stdin=slave,stdout=slave,stderr=slave,env=env);os.close(slave)
            def wait_for(term,seconds=15):
                global output,evidence
                deadline=time.monotonic()+seconds
                while time.monotonic()<deadline:
                    if term.encode() in output:return
                    ready,_,_=select.select([master],[],[],.1)
                    if ready:
                        try:data=os.read(master,65536)
                        except OSError:break
                        output+=data;evidence+=data
                raise AssertionError(f'missing {term!r} in {output[-9000:]!r}')
            def send(value):
                os.write(master,value.encode());time.sleep(.15);os.write(master,b'\r')
            wait_for(mode);time.sleep(.3)
            if mode=='acceptEdits':
                send('exec');wait_for('批准 y')
                # Inspect the rendered terminal bytes, not merely the approval prompt.
                clean=re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]',b'',output).decode(errors='replace')
                for value in ['NON-SANDBOXED','current OS-user access','descendants','"argv"','-c',"exec-marker.txt",'fixture exec output',info['workspace'],'"environment_changes"','FIXTURE_VALUE','safe value','C.UTF-8','/usr/bin:/bin','"timeout_ms": 1000','"output_limit_bytes": 4096','"executable_sha256"','"termination_scope"']:
                    assert value in clean,(value,clean[-9000:])
                assert r"printf 'once\\n' >> exec-marker.txt; printf 'fixture exec output\\n'" in clean
                assert '"program": "'+os.path.realpath('/bin/sh')+'"' in clean
                assert '"cwd": "'+info['workspace']+'"' in clean
                output=b'';os.write(master,b'n');wait_for('failed');assert not Path(info['workspace'],'exec-marker.txt').exists()
                output=b'';send('exec');wait_for('批准 y');output=b'';os.write(master,b'y');time.sleep(.01);os.write(master,b'y');wait_for('工具结果已收到');wait_for('completed');assert Path(info['workspace'],'exec-marker.txt').read_text()=='once\n'
                # Repeated key after stream repaint cannot start another process.
                time.sleep(.2);assert Path(info['workspace'],'exec-marker.txt').read_text()=='once\n'
                # Clear a possible harmless second y in composer before exit.
                os.write(master,b'\x7f');time.sleep(.1)
            else:
                wait_for('文件工具不可用');wait_for('Fixture simulation');wait_for('Not native macOS/Windows evidence');wait_for('Explicit chat-only: no workspace authorization')
                output=b'';send('exec');wait_for('completed');assert b'\xe6\x89\xb9\xe5\x87\x86 y' not in output
                assert not Path(info['workspace'],'exec-marker.txt').exists()
            os.write(master,b'\x03')
            deadline=time.monotonic()+10
            while proc.poll() is None and time.monotonic()<deadline:
                ready,_,_=select.select([master],[],[],.1)
                if ready:
                    try:evidence+=os.read(master,65536)
                    except OSError:break
            proc.wait(timeout=2);assert proc.returncode==0
            Path('/tmp/task6b-acceptance-pty-'+mode+'.log').write_bytes(evidence)
            print('PASS real PTY:',mode,'exact review / safe effects / capability display')
        finally:
            Path('/tmp/task6b-acceptance-pty-'+mode+'.log').write_bytes(evidence)
            if proc and proc.poll() is None:proc.kill();proc.wait(timeout=10)
            if master is not None:os.close(master)
            if server.poll() is None:
                server.send_signal(signal.SIGINT)
                try:server.wait(timeout=10)
                except subprocess.TimeoutExpired:server.kill();server.wait(timeout=10)
