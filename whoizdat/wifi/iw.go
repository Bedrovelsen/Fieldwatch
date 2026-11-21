package wifi

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Monitor puts an interface into monitor mode with ip and iw. It needs
// CAP_NET_ADMIN. Run it once as root; capture itself needs only CAP_NET_RAW.
func Monitor(name string) error {
	for _, args := range [][]string{
		{"ip", "link", "set", "dev", name, "down"},
		{"iw", "dev", name, "set", "type", "monitor"},
		{"ip", "link", "set", "dev", name, "up"},
	} {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// ParseChannels reads "1,6,11,36-48" style lists. A range steps by 1 below
// 15 and by 4 above.
func ParseChannels(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, fmt.Errorf("bad channel %q", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return nil, fmt.Errorf("bad channel range %q", part)
			}
		}
		step := 1
		if a > 14 {
			step = 4
		}
		for c := a; c <= b; c += step {
			out = append(out, c)
		}
	}
	return out, nil
}

// Hop changes the channel of an interface in a loop with iw. A channel that
// the adapter refuses is dropped from the list. It returns when ctx ends or
// no channel is left.
func Hop(ctx context.Context, name string, chans []int, dwell time.Duration) {
	if len(chans) < 2 {
		if len(chans) == 1 {
			setChannel(name, chans[0])
		}
		return
	}
	t := time.NewTicker(dwell)
	defer t.Stop()
	for i := 0; len(chans) > 0; {
		i %= len(chans)
		if err := setChannel(name, chans[i]); err != nil {
			log.Printf("wifi: drop channel %d: %v", chans[i], err)
			chans = append(chans[:i:i], chans[i+1:]...)
			continue
		}
		i++
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func setChannel(name string, c int) error {
	out, err := exec.Command("iw", "dev", name, "set", "channel", strconv.Itoa(c)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
