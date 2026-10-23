//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// Track only this acceptance run's subprocesses. Native CLIs may create their
// own process groups, so fixture commands also hold per-process lease locks.
var children = struct {
	sync.Mutex
	commands map[*exec.Cmd]bool
	leases   []string
}{commands: map[*exec.Cmd]bool{}}

func startTracked(c *exec.Cmd) error {
	children.Lock()
	defer children.Unlock()
	if e := c.Start(); e != nil {
		return e
	}
	children.commands[c] = true
	return nil
}
func waitTracked(c *exec.Cmd) error {
	e := c.Wait()
	children.Lock()
	delete(children.commands, c)
	children.Unlock()
	return e
}
func runTracked(c *exec.Cmd) error {
	if e := startTracked(c); e != nil {
		return e
	}
	return waitTracked(c)
}
func registerLeaseDir(p string) {
	children.Lock()
	children.leases = append(children.leases, p)
	children.Unlock()
}
func cleanupChildren() {
	children.Lock()
	defer children.Unlock()
	for c := range children.commands {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	for _, dir := range children.leases {
		cleanupLeaseDir(dir)
	}
}
func cleanupLeaseDir(dir string) {
	files, _ := os.ReadDir(dir)
	for _, entry := range files {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil {
			continue
		}
		fd, e := os.OpenFile(filepath.Join(dir, entry.Name()), os.O_RDWR, 0)
		if e != nil {
			continue
		}
		if e = syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e == syscall.EWOULDBLOCK {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		} else if e == nil {
			_ = syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
		}
		fd.Close()
	}
}

func runMockWithLease(mode, name string, args []string) int {
	dir := os.Getenv("SYSTEM_SYNC_FIXTURE_LEASES")
	if dir == "" {
		return mockCommand(mode, name, args)
	}
	os.MkdirAll(dir, 0700)
	p := filepath.Join(dir, fmt.Sprint(os.Getpid()))
	fd, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return 90
	}
	defer fd.Close()
	defer os.Remove(p)
	if e := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX); e != nil {
		return 90
	}
	return mockCommand(mode, name, args)
}
func watchInterrupts() func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			cleanupChildren()
			os.Exit(130)
		case <-done:
		}
	}()
	return func() { signal.Stop(signals); close(done) }
}
