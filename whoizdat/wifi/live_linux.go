//go:build linux

package wifi

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	ethPAll         = 0x0003
	arphrdRadiotap  = 803
	arphrdIEEE80211 = 801
)

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// Live reads frames from a monitor-mode interface with an AF_PACKET socket.
// It needs CAP_NET_RAW. It does not change the interface; see Monitor.
func Live(ctx context.Context, name string, fn func(Packet) bool) error {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return err
	}
	link, err := linkType(name)
	if err != nil {
		return err
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethPAll)))
	if err != nil {
		return fmt.Errorf("open raw socket (needs CAP_NET_RAW): %w", err)
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(ethPAll), Ifindex: ifc.Index}); err != nil {
		return fmt.Errorf("bind %s: %w", name, err)
	}
	// A receive timeout lets the loop see a cancelled context.
	tv := syscall.Timeval{Usec: 250_000}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		return err
	}
	buf := make([]byte, 65536)
	for ctx.Err() == nil {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EINTR {
				continue
			}
			return err
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		if !fn(Packet{At: time.Now(), Link: link, Data: data}) {
			return nil
		}
	}
	return nil
}

func linkType(name string) (int, error) {
	b, err := os.ReadFile("/sys/class/net/" + name + "/type")
	if err != nil {
		return 0, err
	}
	switch strings.TrimSpace(string(b)) {
	case fmt.Sprint(arphrdRadiotap):
		return LinkRadiotap, nil
	case fmt.Sprint(arphrdIEEE80211):
		return LinkDot11, nil
	}
	return 0, fmt.Errorf("%s is not in monitor mode; run: whoizdat wifi -iface %s -monitor (as root), or: sudo iw dev %s set type monitor", name, name, name)
}
