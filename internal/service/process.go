package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"localmedia-unlock/internal/model"
)

var ErrNotRunning = errors.New("kernel is not running")

type process struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
	output *logRing
	err    error
}

type processManager struct {
	mu        sync.Mutex
	proc      *process
	startedAt time.Time
	core      string
	configDir string
	onLog     func(string, string, string)
}

func newProcessManager(configDir string, onLog func(string, string, string)) *processManager {
	return &processManager{configDir: configDir, onLog: onLog}
}

func (p *processManager) start(binary, core, configPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.proc != nil {
		return errors.New("kernel is already running")
	}
	if binary == "" {
		return fmt.Errorf("no binary configured for %s", core)
	}
	args, err := kernelArgs(core, configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "LMU_KERNEL="+core)
	cmd.Dir = p.configDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("capture kernel stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("capture kernel stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start %s: %w", core, err)
	}
	proc := &process{cmd: cmd, cancel: cancel, done: make(chan struct{})}
	p.proc = proc
	p.startedAt = time.Now()
	p.core = core
	proc.output = newLogRing(120)
	go p.drain(proc, core, stdout)
	go p.drain(proc, core, stderr)
	go p.wait(proc, core)
	// Some kernels exit only after startup succeeds. Give the process a short
	// startup window so the API can report a crash instead of a false success.
	select {
	case <-proc.done:
		err := proc.err
		if err == nil {
			err = errors.New("kernel exited during startup")
		}
		return fmt.Errorf("%s failed to start: %w", core, err)
	case <-time.After(300 * time.Millisecond):
	}
	p.log("info", core, fmt.Sprintf("kernel started (pid %d)", cmd.Process.Pid))
	return nil
}

func (p *processManager) drain(proc *process, core string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		level := "info"
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
			level = "error"
		case strings.Contains(lower, "warn"):
			level = "warn"
		case strings.Contains(lower, "debug") || strings.Contains(lower, "trace"):
			level = "debug"
		}
		proc.output.log(level, core, line)
		p.log(level, core, line)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		p.log("warn", core, "read kernel output: "+err.Error())
	}
}

func (p *processManager) wait(proc *process, core string) {
	err := proc.cmd.Wait()
	proc.err = err
	close(proc.done)
	p.mu.Lock()
	if p.proc == proc {
		p.proc = nil
		p.core = ""
		p.startedAt = time.Time{}
	}
	p.mu.Unlock()
	level := "info"
	message := "kernel exited"
	if err != nil {
		level = "error"
		message = "kernel exited: " + err.Error()
	}
	p.log(level, core, message)
}

func (p *processManager) stop(timeout time.Duration) error {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc == nil {
		return ErrNotRunning
	}
	proc.cancel()
	select {
	case <-proc.done:
		return nil
	case <-time.After(timeout):
		if proc.cmd.Process != nil {
			_ = proc.cmd.Process.Kill()
		}
		<-proc.done
		return errors.New("kernel did not stop gracefully; process was killed")
	}
}

func (p *processManager) running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.proc != nil
}

func (p *processManager) info() (string, time.Time, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.proc == nil {
		return "", time.Time{}, 0
	}
	pid := 0
	if p.proc.cmd.Process != nil {
		pid = p.proc.cmd.Process.Pid
	}
	return p.core, p.startedAt, pid
}

func (p *processManager) log(level, source, message string) {
	if p.onLog != nil {
		p.onLog(level, source, message)
	}
}

func kernelArgs(core, configPath string) ([]string, error) {
	switch core {
	case model.CoreMihomo:
		return []string{"-f", configPath}, nil
	case model.CoreSingBox:
		return []string{"run", "-c", configPath}, nil
	case model.CoreXray:
		return []string{"run", "-config", configPath}, nil
	default:
		return nil, fmt.Errorf("unsupported core %q", core)
	}
}
