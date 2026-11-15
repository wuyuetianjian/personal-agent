"""PTY launch environment contract; no CI artifact/source checks are disabled."""
import pathlib
import runpy
import unittest


class PreviewPTYEnvironmentTests(unittest.TestCase):
    def test_interactive_child_only(self):
        source = {'CI': 'true', 'CONTINUOUS_INTEGRATION': '1',
                  'GITHUB_ACTIONS': 'true', 'TERM': 'xterm-256color',
                  'PA_AGENT_TOKEN': 'synthetic-test-token'}
        original = source.copy()
        module = runpy.run_path(str(pathlib.Path(__file__).with_name('preview-pty.py')))
        child = module['interactive_environment'](source)
        self.assertEqual(source, original)
        self.assertEqual(child['CI'], 'false')
        self.assertEqual(child['CONTINUOUS_INTEGRATION'], 'false')
        for key in ('GITHUB_ACTIONS', 'TERM', 'PA_AGENT_TOKEN'):
            self.assertEqual(child[key], source[key])

    def test_enter_waits_for_complete_draft_render(self):
        module = runpy.run_path(str(pathlib.Path(__file__).with_name('preview-pty.py')))
        events = []
        module['submit_rendered_draft']('你好', lambda b: events.append(('write', b)),
                                          lambda s: events.append(('render', s)))
        self.assertEqual(events, [('write', '你好'.encode()), ('render', '你好▏'), ('write', b'\r')])

    def test_missing_echo_never_sends_enter(self):
        module = runpy.run_path(str(pathlib.Path(__file__).with_name('preview-pty.py')))
        writes = []
        def missing(_):
            raise AssertionError('draft did not render')
        with self.assertRaises(AssertionError):
            module['submit_rendered_draft']('/exit', writes.append, missing)
        self.assertEqual(writes, [b'/exit'])


if __name__ == '__main__':
    unittest.main()
