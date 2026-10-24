package terminal

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// Foreground saves a caller's controlling terminal before a native command
// temporarily takes ownership of it in its own process group.
type Foreground struct {
	File  *os.File
	group int32
	state *syscall.Termios
}

func PrepareForeground() (*Foreground, error) {
	file, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) || errors.Is(err, os.ErrNotExist) {
			return nil, nil // No controlling terminal (CI or detached sessions).
		}
		return nil, err
	}
	var group int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&group)))
	if errno != 0 {
		file.Close()
		return nil, errno
	}
	if int(group) != syscall.Getpgrp() {
		file.Close()
		return nil, nil // A background caller must not steal the terminal.
	}
	state, err := readState(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &Foreground{File: file, group: group, state: state}, nil
}

// Restore also repairs echo when cancellation kills a password-reading command
// before it can restore its own terminal settings.
func (f *Foreground) Restore() error {
	defer f.File.Close()
	// The caller is in the background until TIOCSPGRP completes. Suppress job
	// control only during restoration, after the native command has exited.
	ignored := signal.Ignored(syscall.SIGTTOU)
	signal.Ignore(syscall.SIGTTOU)
	defer func() {
		if !ignored {
			signal.Reset(syscall.SIGTTOU)
		}
	}()
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.File.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&f.group)))
	if errno != 0 {
		return errno
	}
	return writeState(f.File, f.state)
}
