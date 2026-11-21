//go:build !linux

package wifi

import (
	"context"
	"errors"
	"runtime"
)

// Live is only on Linux. On macOS, read a capture stream instead, for example:
//
//	sudo tcpdump -I -i en0 -w - | whoizdat wifi -pcap -
//
// or run the sensor on a Linux computer and pipe it over ssh.
func Live(ctx context.Context, name string, fn func(Packet) bool) error {
	return errors.New("live capture is not available on " + runtime.GOOS + "; use -pcap - with tcpdump, or a Linux sensor over ssh")
}
