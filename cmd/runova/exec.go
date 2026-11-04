package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// execFunc runs an external command. Tests replace it; osExec is the real one.
type execFunc func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error

func osExec(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// console is the terminal a maintenance command may ask its question on.
type console struct {
	In          io.Reader
	Interactive bool
}

func stdConsole() console {
	return console{In: os.Stdin, Interactive: isTerminal(os.Stdin)}
}

// isTerminal reports whether f is a terminal. A character device is not
// enough: /dev/null is one, and is what a command run without a tty gets.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

var errNoTerminal = errors.New("Tidak ada terminal untuk bertanya; tambahkan --yes")

// confirm asks "Lanjutkan? [y/N]" and reports whether the answer was yes. With
// yes set it does not ask; without a terminal it fails instead of guessing.
func (c console) confirm(stdout io.Writer, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !c.Interactive {
		return false, errNoTerminal
	}
	fmt.Fprint(stdout, "Lanjutkan? [y/N] ")
	line, _ := bufio.NewReader(c.In).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "ya" || answer == "yes", nil
}
