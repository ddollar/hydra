package wan

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"time"

	"github.com/ddollar/ddl"
	"github.com/ddollar/logger"
	probing "github.com/prometheus-community/pro-bing"
	"github.com/vishvananda/netlink"
)

type Wan struct {
	Healthcheck string
	Interfaces  []string
	Interval    time.Duration
	active      string
}

func New(interfaces []string) *Wan {
	return &Wan{
		Interfaces: interfaces,
	}
}

func (w *Wan) Active() string {
	return w.active
}

func (w *Wan) Watch(ctx context.Context) {
	ctx = logger.FromContext(ctx).Append("pkg=wan").At("watch").WithContext(ctx)

	w.check(ctx)

	t := time.NewTicker(w.Interval)
	defer t.Stop()

	for range t.C {
		w.check(ctx)
	}
}

func (w *Wan) check(ctx context.Context) {
	log := logger.FromContext(ctx).Append("interfaces=%v", w.Interfaces)

	for _, iface := range w.Interfaces {
		log := log.Append("interface=%s", iface)

		c, err := w.connected(log, iface)
		if err != nil {
			_ = log.Error(err)
			continue
		}
		if !c {
			log.Logf("status=down")
		}

		if c {
			if iface != w.active {
				log.Logf("active")

				for _, target := range w.Interfaces {
					if err := setMetric(target, ddl.If(target == iface, 100, 101)); err != nil {
						log.Error(err)
					}
				}

				w.active = iface
			}

			break
		}
	}
}

func (w *Wan) connected(log *logger.Logger, iface string) (bool, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return false, err
	}

	if ifc.Flags&net.FlagUp == 0 {
		return false, nil
	}

	p, err := w.pinger(iface)
	if err != nil {
		return false, err
	}

	if err := p.Run(); err != nil {
		return false, err
	}

	if p.Statistics().PacketsRecv == 0 {
		return false, nil
	}

	log.Logf("status=up rtt=%dms", p.Statistics().AvgRtt.Milliseconds())

	return true, nil
}

func (w *Wan) pinger(iface string) (*probing.Pinger, error) {
	p, err := probing.NewPinger(w.Healthcheck)
	if err != nil {
		return nil, err
	}

	p.SetPrivileged(true)

	p.InterfaceName = iface
	p.Count = 5
	p.TTL = 64
	p.Interval = 100 * time.Millisecond
	p.Timeout = 1 * time.Second

	return p, nil
}

func shell(cmd string, args ...string) error {
	return exec.Command(cmd, args...).Run()
}

func setMetric(iface string, priority int) error {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return err
	}

	_, ipn, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		return err
	}

	filter := &netlink.Route{
		LinkIndex: ifc.Index,
		Dst:       ipn,
	}

	rs, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, filter, netlink.RT_FILTER_OIF|netlink.RT_FILTER_DST)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return nil
	}
	if len(rs) > 1 {
		return fmt.Errorf("can not handle multiple default routes for interface: %s", iface)
	}

	r := rs[0]

	if err := shell("route", "del", "default", "dev", iface, "gw", r.Gw.String(), "metric", fmt.Sprintf("%d", r.Priority)); err != nil {
		return err
	}

	if err := shell("route", "add", "default", "dev", iface, "gw", r.Gw.String(), "metric", fmt.Sprintf("%d", priority)); err != nil {
		return err
	}

	if err := shell("ip", "route", "flush", "cache"); err != nil {
		return err
	}

	return nil
}

func address(ifc *net.Interface) (string, error) {
	as, err := ifc.Addrs()
	if err != nil {
		return "", err
	}

	if len(as) < 1 {
		return "", nil
	}

	for _, a := range as {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil {
			return ip.IP.String(), nil
		}
	}

	return "", nil
}
