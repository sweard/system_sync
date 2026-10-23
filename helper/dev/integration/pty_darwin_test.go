//go:build integration && darwin

package integration

import (
	"os"
	"syscall"
	"unsafe"
)

// Match openpty semantics: stdin is a PTY, output remains a pipe, and the PTY
// is not the controlling terminal of the child or its native subprocesses.
func openPTY() (*os.File, *os.File, error) {
	fd, e := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if e != nil {
		return nil, nil, e
	}
	syscall.CloseOnExec(fd)
	master := os.NewFile(uintptr(fd), "pty-master")
	fail := func(e error) (*os.File, *os.File, error) { master.Close(); return nil, nil, e }
	for _, op := range []uintptr{0x20007454, 0x20007452} {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), op, 0)
		if errno != 0 {
			return fail(errno)
		}
	}
	var name [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x40807453, uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		return fail(errno)
	}
	n := 0
	for n < len(name) && name[n] != 0 {
		n++
	}
	slave, e := os.OpenFile(string(name[:n]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if e != nil {
		return fail(e)
	}
	return master, slave, nil
}
