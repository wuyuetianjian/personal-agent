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


if __name__ == '__main__':
    unittest.main()
