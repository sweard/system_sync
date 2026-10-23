//go:build integration && linux

package integration

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func openPTY() (*os.File, *os.File, error) {
	fd, e := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if e != nil {
		return nil, nil, e
	}
	syscall.CloseOnExec(fd)
	master := os.NewFile(uintptr(fd), "pty-master")
	fail := func(e error) (*os.File, *os.File, error) { master.Close(); return nil, nil, e }
	var unlock uint32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x40045431, uintptr(unsafe.Pointer(&unlock)))
	if errno != 0 {
		return fail(errno)
	}
	var number uint32
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x80045430, uintptr(unsafe.Pointer(&number)))
	if errno != 0 {
		return fail(errno)
	}
	slave, e := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if e != nil {
		return fail(e)
	}
	return master, slave, nil
}
