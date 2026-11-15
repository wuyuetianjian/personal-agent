"""Actual relocated Ink render + streamed answer; POSIX only, synthetic server."""
import os, pty, subprocess, sys, select, time, fcntl, termios, struct


def interactive_environment(source):
    """Select Ink's interactive renderer only for the actual PTY child.

    Ink 6.6.0's is-in-ci checks CI/CONTINUOUS_INTEGRATION even with a TTY.
    Keep GITHUB_ACTIONS and all parent artifact/source validation untouched.
    """
    return {**source, 'CI': 'false', 'CONTINUOUS_INTEGRATION': 'false'}


def submit_rendered_draft(text, write, wait_for):
    # A fixed delay does not prevent stdin from coalescing text and Return.
    # Observe the complete application draft (including its cursor) first.
    write(text.encode())
    wait_for(text + '▏')
    write(b'\r')


def main():
    cli, url = sys.argv[1:]
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
    proc = subprocess.Popen(['node', cli, '--url', url, '--mode', 'chat_only'], stdin=slave, stdout=slave, stderr=slave, env=interactive_environment(os.environ))
    os.close(slave)
    output = b''
    def wait_for(text, timeout=20):
        nonlocal output
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if text.encode() in output:
                return
            if select.select([master], [], [], .1)[0]:
                try: output += os.read(master, 65536)
                except OSError: break
            if proc.poll() is not None: break
        raise AssertionError(f'missing {text!r}: {output[-4000:]!r}')
    try:
        wait_for('chat_only')
        submit_rendered_draft('你好', lambda b: os.write(master, b), wait_for)
        wait_for('真实流式测试完成。')
        submit_rendered_draft('/exit', lambda b: os.write(master, b), wait_for)
        deadline = time.monotonic() + 10
        while proc.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], .1)[0]:
                try: output += os.read(master, 65536)
                except OSError: break
        assert proc.wait(timeout=1) == 0, output[-4000:]
        print('PASS: unpacked Ink real POSIX PTY rendered, streamed Chinese answer, exited')
    finally:
        if proc.poll() is None: proc.kill(); proc.wait(timeout=5)
        os.close(master)


if __name__ == '__main__':
    main()
