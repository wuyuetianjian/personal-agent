"""Real Ink draft regression. Separate Enter is intentional, not a paste parser."""
import fcntl, json, os, pty, select, signal, struct, subprocess, tempfile, termios, time
from pathlib import Path

with tempfile.TemporaryDirectory(prefix='draft-render-') as td:
    binary = str(Path(td, 'fixture'))
    subprocess.run(['go', 'build', '-o', binary, './cmd/agent-fixture'], check=True)
    env = {**os.environ, 'PA_AGENT_TOKEN': 'draft-render-test', 'TERM': 'xterm-256color'}
    server = subprocess.Popen([binary, '--chat-only-fixture'], env=env, stdout=subprocess.PIPE, text=True)
    proc = None
    master = None
    try:
        info = json.loads(server.stdout.readline())
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 100, 0, 0))
        proc = subprocess.Popen(['node', '--import', './apps/terminal/node_modules/tsx/dist/loader.mjs',
                                 'apps/terminal/src/cli.tsx', '--url', info['url'], '--mode', 'chat_only'],
                                stdin=slave, stdout=slave, stderr=slave,
                                env={**env, 'CI': 'false', 'CONTINUOUS_INTEGRATION': 'false'})
        os.close(slave)
        output = bytearray()
        def expect(text, timeout=15):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                if text.encode() in output:
                    return
                if select.select([master], [], [], .1)[0]:
                    try:
                        output.extend(os.read(master, 65536))
                    except OSError:
                        break
            raise AssertionError(f'missing complete draft/response {text!r}: {bytes(output[-6000:])!r}')
        expect('Enter 发送')
        output.clear()
        os.write(master, '你好'.encode())
        expect('你好▏')  # Must render both CJK glyphs and caret before any submit.
        output.clear()
        os.write(master, b'\r')
        expect('真实流式测试完成。')
        output.clear()
        os.write(master, '/exit'.encode())
        expect('/exit▏')  # Empty-after-submit -> nonempty also remains intact.
        os.write(master, b'\r')
        deadline = time.monotonic() + 10
        while proc.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], .1)[0]:
                try:
                    os.read(master, 65536)
                except OSError:
                    break
        proc.wait(timeout=1)
        assert proc.returncode == 0, proc.returncode
        print('PASS: real Terminal empty draft -> complete 你好▏ -> separate Enter/stream -> /exit▏')
    finally:
        if proc is not None and proc.poll() is None:
            proc.kill()
            proc.wait()
        if master is not None:
            os.close(master)
        server.send_signal(signal.SIGINT)
        server.wait(timeout=10)
