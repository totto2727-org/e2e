package driver

import (
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTermiosNormalizesEquivalentInputSpeed(t *testing.T) {
	terminal := terminalFor(t, `read value`, nil)
	fd := int(terminal.slave.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	implicit := *original
	implicit.Cflag &^= unix.CIBAUD
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &implicit); err != nil {
		t.Fatal(err)
	}
	inherited, err := termSettings(fd)
	if err != nil {
		t.Fatal(err)
	}
	explicit := implicit
	explicit.Cflag |= (implicit.Cflag & unix.CBAUD) << 16
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &explicit); err != nil {
		t.Fatal(err)
	}
	equal, err := termSettings(fd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inherited, equal) {
		t.Fatalf("equal effective baud rates differ: inherited=%#v explicit=%#v", inherited, equal)
	}
	observed, err := terminal.observe()
	if err != nil {
		t.Fatal(err)
	}
	if !observed.Restored {
		t.Fatal("explicit equal input speed reported unrestored")
	}

	distinct := implicit
	inputSpeed := uint32(unix.B9600)
	if inputSpeed == implicit.Cflag&unix.CBAUD {
		inputSpeed = unix.B19200
	}
	distinct.Cflag |= inputSpeed << 16
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &distinct); err != nil {
		t.Fatal(err)
	}
	changed, err := termSettings(fd)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(inherited, changed) || changed[4] != inputSpeed || changed[5] != (implicit.Cflag&unix.CBAUD) {
		t.Fatalf("distinct input speed lost: %#v", changed)
	}
	observed, err = terminal.observe()
	if err != nil {
		t.Fatal(err)
	}
	if observed.Restored {
		t.Fatal("distinct input speed reported restored")
	}

	t.Run("custom speed retains encoding", func(t *testing.T) {
		custom := implicit
		custom.Cflag &^= unix.CBAUD | unix.CIBAUD
		custom.Cflag |= unix.BOTHER | unix.BOTHER<<16
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, &custom); err != nil {
			t.Fatal(err)
		}
		raw, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			t.Fatal(err)
		}
		if raw.Cflag&unix.CIBAUD != unix.BOTHER<<16 {
			t.Fatalf("custom input encoding not retained by kernel: %#x", raw.Cflag)
		}
		normalized, err := termSettings(fd)
		if err != nil {
			t.Fatal(err)
		}
		if normalized[2] != raw.Cflag || normalized[4] != uint32(unix.BOTHER) || normalized[5] != uint32(unix.BOTHER) {
			t.Fatalf("unresolved custom speed was normalized: %#v", normalized)
		}
	})

	for _, flag := range []uint32{unix.ECHO, unix.ICANON} {
		changedFlags := implicit
		changedFlags.Lflag ^= flag
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, &changedFlags); err != nil {
			t.Fatal(err)
		}
		observed, err = terminal.observe()
		if err != nil {
			t.Fatal(err)
		}
		if observed.Restored {
			t.Fatalf("changed lflag %#x reported restored", flag)
		}
	}
}
