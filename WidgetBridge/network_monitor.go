package main

import (
	"context"
	"fmt"
	stdnet "net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	gopsutilnet "github.com/shirou/gopsutil/v4/net"
)

var pingTimePattern = regexp.MustCompile(`(?i)(?:time|tempo)[=<]?\s*(\d+)\s*ms`)
var wifiSSIDPattern = regexp.MustCompile(`(?mi)^\s*SSID(?:\s+\d+)?\s*:\s*(.+?)\s*$`)

type networkStatusResponse struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	Metrics networkSnapshot `json:"metrics"`
}

type networkSnapshot struct {
	DownloadBps float64 `json:"downloadBps"`
	UploadBps   float64 `json:"uploadBps"`
	PingMs      int     `json:"pingMs"`
	PingTarget  string  `json:"pingTarget"`
	Online      bool    `json:"online"`
	UpdatedAt   string  `json:"updatedAt"`
	Interface   string  `json:"interface,omitempty"`
	SSID        string  `json:"ssid,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type networkTotals struct {
	received uint64
	sent     uint64
	primary  string
}

type networkMonitor struct {
	target string

	mu      sync.RWMutex
	current networkSnapshot

	stopCh chan struct{}
	doneCh chan struct{}
}

func newNetworkMonitor(target string) *networkMonitor {
	monitor := &networkMonitor{
		target: target,
		current: networkSnapshot{
			PingTarget: target,
			UpdatedAt:  time.Now().Format(time.RFC3339),
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	go monitor.run()
	return monitor
}

func (monitor *networkMonitor) run() {
	defer close(monitor.doneCh)

	monitor.refreshPing()
	monitor.refreshWiFiSSID()

	previousTotals, err := readNetworkTotals()
	if err != nil {
		monitor.setError(err)
	}

	throughputTicker := time.NewTicker(1 * time.Second)
	defer throughputTicker.Stop()

	pingTicker := time.NewTicker(5 * time.Second)
	defer pingTicker.Stop()

	wifiTicker := time.NewTicker(8 * time.Second)
	defer wifiTicker.Stop()

	for {
		select {
		case <-monitor.stopCh:
			return
		case <-throughputTicker.C:
			currentTotals, readErr := readNetworkTotals()
			if readErr != nil {
				monitor.setError(readErr)
				continue
			}

			downloadBps := computeBps(currentTotals.received, previousTotals.received)
			uploadBps := computeBps(currentTotals.sent, previousTotals.sent)
			monitor.setThroughput(downloadBps, uploadBps, currentTotals.primary)
			previousTotals = currentTotals
		case <-pingTicker.C:
			monitor.refreshPing()
		case <-wifiTicker.C:
			monitor.refreshWiFiSSID()
		}
	}
}

func (monitor *networkMonitor) stop() {
	select {
	case <-monitor.stopCh:
	case <-monitor.doneCh:
		return
	default:
		close(monitor.stopCh)
	}

	<-monitor.doneCh
}

func (monitor *networkMonitor) snapshot() networkSnapshot {
	monitor.mu.RLock()
	defer monitor.mu.RUnlock()

	return monitor.current
}

func (monitor *networkMonitor) setThroughput(downloadBps, uploadBps float64, primaryInterface string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.DownloadBps = downloadBps
	monitor.current.UploadBps = uploadBps
	monitor.current.Interface = primaryInterface
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
	if monitor.current.Error == "" {
		monitor.current.Online = true
	}
}

func (monitor *networkMonitor) setPing(pingMs int, err error) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.PingTarget = monitor.target
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
	if err != nil {
		monitor.current.PingMs = 0
		monitor.current.Online = false
		monitor.current.Error = err.Error()
		return
	}

	monitor.current.PingMs = pingMs
	monitor.current.Online = true
	monitor.current.Error = ""
}

func (monitor *networkMonitor) setError(err error) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.Error = err.Error()
	monitor.current.Online = false
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
}

func (monitor *networkMonitor) refreshPing() {
	pingMs, err := measurePing(monitor.target)
	monitor.setPing(pingMs, err)
}

func (monitor *networkMonitor) refreshWiFiSSID() {
	monitor.setWiFiSSID(readWiFiSSID())
}

func (monitor *networkMonitor) setWiFiSSID(ssid string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.SSID = ssid
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
}

func computeBps(current, previous uint64) float64 {
	if current < previous {
		return 0
	}

	return float64(current - previous)
}

func readNetworkTotals() (networkTotals, error) {
	interfaces, err := stdnet.Interfaces()
	if err != nil {
		return networkTotals{}, fmt.Errorf("list interfaces: %w", err)
	}

	loopbackNames := make(map[string]struct{}, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&stdnet.FlagLoopback != 0 {
			loopbackNames[iface.Name] = struct{}{}
		}
	}

	stats, err := gopsutilnet.IOCounters(true)
	if err != nil {
		return networkTotals{}, fmt.Errorf("read network counters: %w", err)
	}

	type interfaceUsage struct {
		name  string
		total uint64
	}

	var totals networkTotals
	usage := make([]interfaceUsage, 0, len(stats))
	for _, stat := range stats {
		if _, skip := loopbackNames[stat.Name]; skip {
			continue
		}
		if isVirtualInterface(stat.Name) {
			continue
		}

		totals.received += stat.BytesRecv
		totals.sent += stat.BytesSent
		usage = append(usage, interfaceUsage{name: stat.Name, total: stat.BytesRecv + stat.BytesSent})
	}

	if len(usage) == 0 {
		return networkTotals{}, fmt.Errorf("no active network interfaces found")
	}

	sort.SliceStable(usage, func(i, j int) bool {
		return usage[i].total > usage[j].total
	})
	totals.primary = usage[0].name

	return totals, nil
}

func isVirtualInterface(name string) bool {
	lowered := strings.ToLower(strings.TrimSpace(name))
	if lowered == "" {
		return true
	}

	virtualMarkers := []string{"loopback", "virtual", "vmware", "vbox", "vethernet", "hyper-v", "bluetooth", "isatap", "teredo"}
	for _, marker := range virtualMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}

	return false
}

func measurePing(target string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping.exe", "-n", "1", "-w", "900", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	text := string(output)
	if match := pingTimePattern.FindStringSubmatch(text); len(match) == 2 {
		pingMs, convErr := strconv.Atoi(match[1])
		if convErr == nil {
			return pingMs, nil
		}
	}

	if err != nil {
		return 0, fmt.Errorf("ping %s: %w", target, err)
	}

	return 0, fmt.Errorf("ping %s: latency not found in output", target)
}

func readWiFiSSID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netsh.exe", "wlan", "show", "interfaces")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	for _, match := range wifiSSIDPattern.FindAllStringSubmatch(string(output), -1) {
		if len(match) != 2 {
			continue
		}

		ssid := strings.TrimSpace(match[1])
		if ssid == "" || strings.HasPrefix(strings.ToUpper(ssid), "BSSID") {
			continue
		}

		return ssid
	}

	return ""
}
