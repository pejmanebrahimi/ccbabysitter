package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func init() { startTrustTerminal = startPTY }

// ptyTerminal is the CLI started by this program on a pseudo-terminal of
// its own, in a session of its own.
type ptyTerminal struct {
	master *os.File
	cmd    *exec.Cmd
	out    chan []byte
	exited chan struct{}
	// done closes when End starts, so the reader stops handing on the
	// screen nobody reads any more and returns.
	done chan struct{}
}

// startPTY opens a pseudo-terminal, starts bin on it in dir as the leader
// of a new session, and reads its screen until it exits.
func startPTY(ctx context.Context, bin, dir string) (trustTerminal, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open a pseudo-terminal: %w", err)
	}
	fail := func(err error) (trustTerminal, error) {
		master.Close()
		return nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return fail(fmt.Errorf("unlock the pseudo-terminal: %w", err))
	}
	n, err := unix.IoctlGetUint32(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return fail(fmt.Errorf("name the pseudo-terminal: %w", err))
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fail(fmt.Errorf("open the pseudo-terminal's other side: %w", err))
	}
	defer slave.Close()
	// A screen large enough that the question is drawn whole.
	_ = unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120})

	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("start %s: %w", bin, err))
	}
	t := &ptyTerminal{master: master, cmd: cmd, out: make(chan []byte, 64), exited: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(t.out)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				select {
				case t.out <- append([]byte(nil), buf[:n]...):
				case <-t.done:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		close(t.exited)
	}()
	return t, nil
}

func (t *ptyTerminal) Write(p []byte) (int, error) { return t.master.Write(p) }

func (t *ptyTerminal) Output() <-chan []byte { return t.out }

// End ends the CLI this program started, and anything it started in its
// session, asking first and forcing after two seconds, then waits for it.
// It is called once.
// The process is the one cmd started and is known by its own handle, never
// looked up by name.
func (t *ptyTerminal) End() error {
	close(t.done)
	defer t.master.Close()
	pid := t.cmd.Process.Pid
	select {
	case <-t.exited:
		// The CLI has gone; anything it left behind in its session goes too.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return nil
	default:
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-t.exited:
		return nil
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	<-t.exited
	return nil
}
