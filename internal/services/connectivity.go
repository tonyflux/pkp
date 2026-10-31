package services

import (
	"context"
	"net/url"
	"sync"
	"time"

	"github.com/tonyflux/pkp/internal/drive"
	"github.com/tonyflux/pkp/internal/netproxy"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const EventConnectivityState = "network:connectivity"

type ConnectivityTarget struct {
	Service   string `json:"service"`
	Host      string `json:"host"`
	Via       string `json:"via"`
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status"`
	ElapsedMs int64  `json:"elapsedMs"`
	Node      string `json:"node"`
}

type ConnectivityResult struct {
	At      string               `json:"at"`
	Poor    bool                 `json:"poor"`
	Targets []ConnectivityTarget `json:"targets"`
}

// TestConnectivity tests the applied route, never an unsaved proxy draft.
func (d *DriveService) TestConnectivity() ConnectivityResult {
	return d.checkConnectivity(context.Background())
}

// Connectivity reads the last automatic or manual result without network I/O.
func (d *DriveService) Connectivity() ConnectivityResult {
	d.connectivityMu.Lock()
	defer d.connectivityMu.Unlock()
	result := d.connectivity
	result.Targets = append([]ConnectivityTarget{}, result.Targets...)
	return result
}

func (d *DriveService) checkConnectivity(ctx context.Context) ConnectivityResult {
	d.connectivityRun.Lock()
	defer d.connectivityRun.Unlock()
	cfg := netproxy.Current()
	auth := d.opts.AuthBaseURL
	if auth == "" {
		auth = drive.DefaultAuthBaseURL
	}
	targets := []struct{ service, address string }{{"files", d.apiBaseURL()}, {"auth", auth}, {"webdav", drive.DefaultDAVBaseURL}}
	result := ConnectivityResult{At: time.Now().UTC().Format(time.RFC3339), Targets: make([]ConnectivityTarget, len(targets))}
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := url.Parse(target.address)
			if err != nil || u.Host == "" {
				result.Targets[i] = ConnectivityTarget{Service: target.service}
				return
			}
			// Expose no URL credentials, queries, proxy credentials or account data.
			u.User = nil
			u.RawQuery = ""
			u.Fragment = ""
			timeout, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			measured := netproxy.Measure(timeout, cfg, u.String())
			via := proxyWhere(netproxy.Config{Mode: netproxy.ModeDirect}, netproxy.System())
			if endpoint, e := cfg.ProxyFor(u); e != nil {
				via = ""
			} else if endpoint != nil {
				via = endpointWhere(endpoint)
			}
			result.Targets[i] = ConnectivityTarget{Service: target.service, Host: u.Host, Via: via, Reachable: measured.Reachable, Status: measured.Status, ElapsedMs: measured.ElapsedMs, Node: measured.Node}
		}()
	}
	wg.Wait()
	for _, target := range result.Targets {
		if !target.Reachable || target.ElapsedMs >= 2000 || target.Status == 429 || target.Status >= 500 {
			result.Poor = true
		}
	}
	// A result from an old route must not become the new route's diagnosis.
	if ctx.Err() != nil || !sameProxyConfig(cfg, netproxy.Current()) {
		return ConnectivityResult{Targets: []ConnectivityTarget{}}
	}
	d.connectivityMu.Lock()
	d.connectivity = result
	d.connectivityMu.Unlock()
	if app := application.Get(); app != nil {
		app.Event.Emit(EventConnectivityState, result)
	}
	return result
}

func sameProxyConfig(a, b netproxy.Config) bool {
	return a.Mode == b.Mode && a.Scheme == b.Scheme && a.Host == b.Host && a.Port == b.Port && a.Username == b.Username && a.Password == b.Password && netproxy.FormatBypass(a.Bypass) == netproxy.FormatBypass(b.Bypass)
}

func (d *DriveService) watchConnectivity(parent context.Context) {
	d.connectivityMu.Lock()
	if d.connectivityCancel != nil {
		d.connectivityMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	d.connectivityCancel = cancel
	d.connectivityWake = make(chan struct{}, 1)
	wake := d.connectivityWake
	d.connectivityWG.Add(1)
	d.connectivityMu.Unlock()
	go func() {
		defer d.connectivityWG.Done()
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				d.checkConnectivity(ctx)
			case <-timer.C:
				d.checkConnectivity(ctx)
				timer.Reset(5 * time.Minute)
			}
		}
	}()
}
