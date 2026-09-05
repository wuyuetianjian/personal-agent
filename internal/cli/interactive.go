package cli

import (
	"bufio"
	"io"
	"os"

	"github.com/chzyer/readline"
)

var slashCommands = []string{"/help", "/memory", "/exit", "/quit"}

func isInteractiveTerminal(streams IO) bool {
	stdin, stdinOK := streams.Stdin.(*os.File)
	stdout, stdoutOK := streams.Stdout.(*os.File)
	if !stdinOK || !stdoutOK {
		return false
	}
	return isCharDevice(stdin) && isCharDevice(stdout)
}

func isCharDevice(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func newReadline(streams IO) (*readline.Instance, error) {
	stdin, _ := streams.Stdin.(*os.File)
	stdout, _ := streams.Stdout.(*os.File)
	return readline.NewEx(&readline.Config{
		Prompt:          "> ",
		Stdin:           stdin,
		Stdout:          stdout,
		Stderr:          stdout,
		HistoryLimit:    200,
		AutoComplete:    slashCommandCompleter(),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
}

func newChatLineReader(streams IO) (lineReader, error) {
	if isInteractiveTerminal(streams) {
		instance, err := newReadline(streams)
		if err != nil {
			return nil, err
		}
		return readlineLineReader{instance: instance}, nil
	}
	return scannerLineReader{
		scanner: &lineScanner{scanner: bufio.NewScanner(streams.Stdin)},
		stdout:  streams.Stdout,
	}, nil
}

type readlineLineReader struct {
	instance *readline.Instance
}

func (r readlineLineReader) ReadLine() (string, error) {
	return r.instance.Readline()
}

func (r readlineLineReader) Close() error {
	return r.instance.Close()
}

func slashCommandCompleter() readline.AutoCompleter {
	items := make([]readline.PrefixCompleterInterface, 0, len(slashCommands))
	for _, command := range slashCommands {
		items = append(items, readline.PcItem(command))
	}
	return readline.NewPrefixCompleter(items...)
}

type lineReader interface {
	ReadLine() (string, error)
	Close() error
}

type scannerLineReader struct {
	scanner *lineScanner
	stdout  io.Writer
}

func (r scannerLineReader) ReadLine() (string, error) {
	if r.stdout != nil {
		r.stdout.Write([]byte("> "))
	}
	return r.scanner.Scan()
}

func (r scannerLineReader) Close() error {
	return nil
}

type lineScanner struct {
	scanner interface {
		Scan() bool
		Text() string
		Err() error
	}
}

func (s *lineScanner) Scan() (string, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return s.scanner.Text(), nil
}

func printChatHelp(stdout io.Writer) {
	stdout.Write([]byte("Commands:\n"))
	for _, command := range slashCommands {
		stdout.Write([]byte("  " + command + "\n"))
	}
}
