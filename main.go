package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/ddollar/hydra/pkg/api"
	"github.com/ddollar/hydra/pkg/config"
	"github.com/ddollar/hydra/pkg/wan"
	"github.com/ddollar/logger"
	"go.bug.st/serial"
)

// var active string
// var healthcheck string
// var interval time.Duration

func main() {
	// flag.DurationVar(&interval, "i", 15*time.Second, "check interval")
	// flag.StringVar(&healthcheck, "h", "1.1.1.1", "healthcheck address")
	// flag.Parse()

	// if len(flag.Args()) < 2 {
	// 	fmt.Fprintf(os.Stderr, "usage: hydra <interface>...\n")
	// 	os.Exit(1)
	// }

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	cfg, err := config.Load("/etc/hydra.yml")
	if err != nil {
		return err
	}

	w, err := runWan(ctx, cfg)
	if err != nil {
		return err
	}

	if err := runApi(ctx, w, cfg); err != nil {
		return err
	}

	select {}
}

func runApi(ctx context.Context, w *wan.Wan, cfg *config.Config) error {
	qhs, err := qualityHandlers(cfg)
	if err != nil {
		return err
	}

	a := api.New(w, qhs)

	go a.Listen(ctx, "https", ":3000")

	return nil
}

func runWan(ctx context.Context, cfg *config.Config) (*wan.Wan, error) {
	interfaces := make([]string, len(cfg.Interfaces))

	for i := range cfg.Interfaces {
		interfaces[i] = cfg.Interfaces[i].Device
	}

	w := wan.New(interfaces)

	w.Healthcheck = cfg.Healthcheck
	w.Interval = cfg.Interval
	w.Timeout = cfg.Timeout

	go w.Watch(logger.New("ns=hydra").WithContext(ctx))

	return w, nil
}

func qualityHandlers(cfg *config.Config) (api.QualityHandlers, error) {
	qhs := api.QualityHandlers{}

	for _, i := range cfg.Interfaces {
		qh, err := qualityHandler(i)
		if err != nil {
			return nil, err
		}

		qhs[i.Device] = qh
	}

	return qhs, nil
}

func qualityHandler(ci config.Interface) (api.QualityHandler, error) {
	switch ci.Type {
	case "gprs":
		return gprsQualityHandler(ci), nil
	case "starlink":
		return starlinkQualityHandler(ci), nil
	case "wifi":
		return wifiQualityHandler(ci), nil
	default:
		return nil, fmt.Errorf("unknown device type: %s", ci.Type)
	}
}

var reMobileSignalStrength = regexp.MustCompile(`\+CSQ:\s*(\d+),\s*(\d+)`)

var dbmTable = map[string]int{
	"0":  -113,
	"1":  -111,
	"2":  -109,
	"3":  -107,
	"4":  -105,
	"5":  -103,
	"6":  -101,
	"7":  -99,
	"8":  -97,
	"9":  -95,
	"10": -93,
	"11": -91,
	"12": -89,
	"13": -87,
	"14": -85,
	"15": -83,
	"16": -81,
	"17": -79,
	"18": -77,
	"19": -75,
	"20": -73,
	"21": -71,
	"22": -69,
	"23": -67,
	"24": -65,
	"25": -63,
	"26": -61,
	"27": -59,
	"28": -57,
	"29": -55,
	"30": -53,
	"31": -51,
	"99": -113, // not known or not detectable
}

func gprsQualityHandler(ci config.Interface) api.QualityHandler {
	return func() (int, error) {
		if ci.Check == "" {
			return 0, fmt.Errorf("check required for gprs")
		}

		s, err := serial.Open(ci.Check, &serial.Mode{
			BaudRate: 115200,
			Parity:   serial.NoParity,
			DataBits: 8,
			StopBits: serial.OneStopBit,
		})
		if err != nil {
			return 0, err
		}
		defer s.Close()

		if _, err := s.Write([]byte("AT+CSQ\r")); err != nil {
			return 0, err
		}

		csq := make(chan string)
		ta := time.After(3 * time.Second)

		go func() {
			scanner := bufio.NewScanner(s)

			for scanner.Scan() {
				m := reMobileSignalStrength.FindStringSubmatch(scanner.Text())

				if len(m) > 2 {
					csq <- m[1]
				}
			}
		}()

		select {
		case <-ta:
			return 0, fmt.Errorf("timeout")
		case id := <-csq:
			if dbm, ok := dbmTable[id]; ok {
				return dbmToPercentage(dbm), nil
			}
			return 0, nil
		}
	}
}

func starlinkQualityHandler(ci config.Interface) api.QualityHandler {
	return func() (int, error) {
		return 0, nil
	}
}

var reWlanSignalStrength = regexp.MustCompile(`signal:\s*(-?\d+)`)

func wifiQualityHandler(ci config.Interface) api.QualityHandler {
	return func() (int, error) {
		data, err := exec.Command("iw", "dev", ci.Device, "link").CombinedOutput()
		if err != nil {
			return 0, err
		}

		m := reWlanSignalStrength.FindStringSubmatch(string(data))
		if len(m) < 2 {
			return 0, fmt.Errorf("signal strength not found")
		}

		ss, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, err
		}

		return dbmToPercentage(ss), nil
	}
}

func dbmToPercentage(dBm int) int {
	if dBm <= -100 {
		return 0
	}

	if dBm >= -50 {
		return 100
	}

	return 2 * (dBm + 100)
}
