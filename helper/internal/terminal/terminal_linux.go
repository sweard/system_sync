package terminal

import (
	"os"
	"syscall"
	"unsafe"
)

func IsTerminal(file *os.File) bool {
	_, err := readState(file)
	return err == nil
}

func readState(file *os.File) (*syscall.Termios, error) {
	var state syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
	if err != 0 {
		return nil, err
	}
	return &state, nil
}

func writeState(file *os.File, state *syscall.Termios) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(state)))
	if err != 0 {
		return err
	}
	return nil
}
