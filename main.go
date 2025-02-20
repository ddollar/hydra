package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/ddollar/ddl"
	"github.com/ddollar/logger"
	probing "github.com/prometheus-community/pro-bing"
	"github.com/vishvananda/netlink"
)

var active string
var healthcheck string
var interval time.Duration

func main() {
	flag.DurationVar(&interval, "i", 15*time.Second, "check interval")
	flag.StringVar(&healthcheck, "h", "1.1.1.1", "healthcheck address")
	flag.Parse()

	if len(flag.Args()) < 2 {
		fmt.Fprintf(os.Stderr, "usage: hydra <interface>...\n")
		os.Exit(1)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	interfaces := flag.Args()

	log := logger.New("ns=hydra")
	log.Logf("interfaces=%v", interfaces)

	check(log, interfaces)

	t := time.NewTicker(interval)
	defer t.Stop()

	for range t.C {
		check(log, interfaces)
	}

	return nil
}

func check(log *logger.Logger, interfaces []string) {
	for _, iface := range interfaces {
		log := log.Append("interface=%s", iface)

		c, err := connected(log, iface)
		if err != nil {
			_ = log.Error(err)
			continue
		}
		if !c {
			log.Logf("status=down")
		}

		if c {
			if iface != active {
				log.Logf("active")

				for _, target := range interfaces {
					if err := setMetric(target, ddl.If(target == iface, 100, 101)); err != nil {
						log.Error(err)
					}
				}

				active = iface
			}

			break
		}
	}
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

func connected(log *logger.Logger, iface string) (bool, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return false, err
	}

	if ifc.Flags&net.FlagUp == 0 {
		return false, nil
	}

	p, err := pinger(iface)
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

func pinger(iface string) (*probing.Pinger, error) {
	p, err := probing.NewPinger(healthcheck)
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
