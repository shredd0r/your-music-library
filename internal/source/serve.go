package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Default lifetime of protocol 2 plugins found by Discover; the app sets
// its configured values with Registry.SetTimeouts.
var (
	// IdleTimeout closes a running plugin that has had no requests for
	// this long; the next request starts it again. 0 keeps it running.
	IdleTimeout = 5 * time.Minute
	// CloseTimeout is how long a plugin gets to exit after close before
	// it is killed.
	CloseTimeout = 5 * time.Second
)

// request runs a command in the plugin's running process, starting it if
// needed.
func (p *Plugin) request(ctx context.Context, command string, req, resp any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pr, err := p.acquire()
	if err != nil {
		return err
	}
	defer p.release(pr)
	answer, err := pr.roundTrip(ctx, command, req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", p.Name, command, err)
	}
	var envelope struct {
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(answer, &envelope); err != nil {
		return fmt.Errorf("%s %s: invalid JSON response: %w", p.Name, command, err)
	}
	if envelope.Error != "" {
		return fmt.Errorf("%s %s: %s", p.Name, command, envelope.Error)
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("%s %s: answer has no result", p.Name, command)
	}
	return p.decode(command, envelope.Result, resp)
}

// acquire returns the running process, starting one if there is none or
// the last one exited, and holds off the idle timer.
func (p *Plugin) acquire() (*process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.proc != nil && p.proc.exitedNow() {
		p.proc = nil
	}
	if p.proc == nil {
		pr, err := startProcess(p.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		p.proc = pr
	}
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	p.active++
	return p.proc, nil
}

// release ends a request; the last one starts the idle timer.
func (p *Plugin) release(pr *process) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
	if p.active == 0 && p.proc == pr && p.idleTimeout > 0 {
		p.idle = time.AfterFunc(p.idleTimeout, func() { p.closeIdle(pr) })
	}
}

// closeIdle closes pr unless a request arrived in the meantime.
func (p *Plugin) closeIdle(pr *process) {
	p.mu.Lock()
	if p.proc != pr || p.active > 0 {
		p.mu.Unlock()
		return
	}
	p.proc, p.idle = nil, nil
	wait := p.closeWait()
	p.mu.Unlock()
	pr.close(wait)
}

// closeWait is the time a plugin gets to exit after close; p.mu is held.
func (p *Plugin) closeWait() time.Duration {
	if p.closeTimeout > 0 {
		return p.closeTimeout
	}
	return CloseTimeout
}

// Close sends close to the running process, if any, and waits until it
// exits (killing it after its close timeout). Requests still in flight fail. A
// later request starts the plugin again.
func (p *Plugin) Close() {
	p.mu.Lock()
	pr := p.proc
	p.proc = nil
	if p.idle != nil {
		p.idle.Stop()
		p.idle = nil
	}
	wait := p.closeWait()
	p.mu.Unlock()
	if pr != nil {
		pr.close(wait)
	}
}

// process is a running `<plugin> serve`: requests and answers are JSON
// lines matched by id.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailWriter

	writeMu sync.Mutex // one request line at a time
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan []byte
	exited  chan struct{} // closed once stdout ends and the process is reaped
	waitErr error         // set before exited is closed
}

func startProcess(path string) (*process, error) {
	cmd := exec.Command(path, "serve")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	pr := &process{cmd: cmd, stdin: stdin, stderr: &tailWriter{n: 16 << 10},
		pending: map[int64]chan []byte{}, exited: make(chan struct{})}
	cmd.Stderr = pr.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	go pr.read(stdout)
	return pr, nil
}

// read hands each answer to the request waiting for its id. Lines with an
// unknown id are ignored.
func (pr *process) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		var head struct {
			ID *int64 `json:"id"`
		}
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &head) == nil && head.ID != nil {
			pr.mu.Lock()
			ch := pr.pending[*head.ID]
			delete(pr.pending, *head.ID)
			pr.mu.Unlock()
			if ch != nil {
				ch <- line // buffered, never blocks
			}
		}
		if err != nil {
			break
		}
	}
	pr.waitErr = pr.cmd.Wait()
	close(pr.exited)
}

func (pr *process) exitedNow() bool {
	select {
	case <-pr.exited:
		return true
	default:
		return false
	}
}

// exitError explains why the process is gone, using its last stderr line.
func (pr *process) exitError() error {
	if msg := lastLine(pr.stderr.String()); msg != "" {
		return fmt.Errorf("plugin exited: %s", msg)
	}
	if pr.waitErr != nil {
		return fmt.Errorf("plugin exited: %w", pr.waitErr)
	}
	return errors.New("plugin exited")
}

// roundTrip sends one request and waits for its answer.
func (pr *process) roundTrip(ctx context.Context, command string, req any) ([]byte, error) {
	ch := make(chan []byte, 1)
	pr.mu.Lock()
	pr.nextID++
	id := pr.nextID
	pr.pending[id] = ch
	pr.mu.Unlock()
	defer func() {
		pr.mu.Lock()
		delete(pr.pending, id)
		pr.mu.Unlock()
	}()

	line, err := json.Marshal(struct {
		ID      int64  `json:"id"`
		Command string `json:"command"`
		Params  any    `json:"params"`
	}{id, command, req})
	if err != nil {
		return nil, err
	}
	pr.writeMu.Lock()
	_, werr := pr.stdin.Write(append(line, '\n'))
	pr.writeMu.Unlock()
	if werr != nil {
		// the process is gone or going; wait for its reason
		select {
		case <-pr.exited:
			return nil, pr.exitError()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case answer := <-ch:
		return answer, nil
	case <-pr.exited:
		select {
		case answer := <-ch: // answered just before exiting
			return answer, nil
		default:
			return nil, pr.exitError()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// close asks the process to stop, closes its stdin, and kills it if it
// is still running after wait.
func (pr *process) close(wait time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, _ = pr.roundTrip(ctx, "close", struct{}{}) // an answer, an exit or the deadline: all mean go on
	_ = pr.stdin.Close()                          // EOF is a second request to stop; closing twice is harmless
	select {
	case <-pr.exited:
		return
	case <-ctx.Done():
	}
	_ = pr.cmd.Process.Kill() // fails only if it exited meanwhile
	select {
	case <-pr.exited:
	case <-time.After(wait): // a child process still holds stdout; give up waiting
	}
}

// tailWriter keeps the last n bytes written.
type tailWriter struct {
	mu  sync.Mutex
	buf []byte
	n   int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.n; extra > 0 {
		t.buf = t.buf[extra:]
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
