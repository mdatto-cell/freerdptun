package tunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Bridge struct {
	Wire *Wire
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	done chan struct{}
	once sync.Once
}

func StartBridge(ctx context.Context, path string, args []string, files []*os.File) (*Bridge, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stderr = os.Stderr
	if err := attachFiles(cmd, files); err != nil {
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	cmd.Cancel = func() error { _ = in.Close(); return nil }
	cmd.WaitDelay = 2 * time.Second
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	p := &Bridge{Wire: NewWire(out, in), cmd: cmd, in: in, out: out, done: make(chan struct{})}
	// Wait must not close stdout before the final buffered bytes are consumed.
	// Command.Wait closes its StdoutPipe; transport is continuous until EOF.
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *Bridge) Close() {
	p.once.Do(func() {
		_ = p.in.Close()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = p.cmd.Process.Kill()
		}
		_ = p.out.Close()
		<-p.done
	})
}
func authTimeout(ctx context.Context, p *Bridge, seconds int) func() {
	return authTimeoutDuration(ctx, p, time.Duration(seconds)*time.Second)
}
func authTimeoutDuration(ctx context.Context, p *Bridge, duration time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-stop:
		case <-ctx.Done():
			p.Close()
		case <-timer.C:
			p.Close()
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}

var errDuplicate = errors.New("UUID already connected")
