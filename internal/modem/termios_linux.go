//go:build linux

package modem

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func baudConst(baud int) (uint32, error) {
	switch baud {
	case 9600:
		return unix.B9600, nil
	case 19200:
		return unix.B19200, nil
	case 38400:
		return unix.B38400, nil
	case 57600:
		return unix.B57600, nil
	case 115200:
		return unix.B115200, nil
	case 230400:
		return unix.B230400, nil
	default:
		return 0, fmt.Errorf("unsupported baud rate %d", baud)
	}
}

func configurePort(fd, baud int) error {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	speed, err := baudConst(baud)
	if err != nil {
		return err
	}
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Ispeed = speed
	t.Ospeed = speed
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}
