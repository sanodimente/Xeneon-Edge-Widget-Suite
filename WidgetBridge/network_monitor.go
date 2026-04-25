package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	stdnet "net"
	"net/http"
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

const networkHistoryLimit = 20

type networkStatusResponse struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	Metrics networkSnapshot `json:"metrics"`
}

type networkHistory struct {
	DownloadBps []float64 `json:"downloadBps"`
	UploadBps   []float64 `json:"uploadBps"`
	PingMs      []int     `json:"pingMs"`
}

type publicIPLookupResponse struct {
	Success bool   `json:"success"`
	IP      string `json:"ip"`
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
	Message string `json:"message"`
}

type radioSnapshot struct {
	Name  string `json:"Name"`
	Kind  any    `json:"Kind"`
	State any    `json:"State"`
}

type networkSnapshot struct {
	DownloadBps float64        `json:"downloadBps"`
	UploadBps   float64        `json:"uploadBps"`
	PingMs      int            `json:"pingMs"`
	PingTarget  string         `json:"pingTarget"`
	Online      bool           `json:"online"`
	UpdatedAt   string         `json:"updatedAt"`
	Interface   string         `json:"interface,omitempty"`
	SSID        string         `json:"ssid,omitempty"`
	Error       string         `json:"error,omitempty"`
	History     networkHistory `json:"history"`
	PublicIP    string         `json:"publicIp,omitempty"`
	Location    string         `json:"location,omitempty"`
	WiFiEnabled bool           `json:"wifiEnabled"`
	WiFiKnown   bool           `json:"wifiKnown"`
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
			History:    newNetworkHistory(),
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
	monitor.refreshWiFiRadio()
	monitor.refreshPublicNetworkInfo()

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

	publicInfoTicker := time.NewTicker(10 * time.Minute)
	defer publicInfoTicker.Stop()

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
			monitor.refreshWiFiRadio()
		case <-publicInfoTicker.C:
			monitor.refreshPublicNetworkInfo()
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

	snapshot := monitor.current
	snapshot.History = cloneNetworkHistory(monitor.current.History)
	return snapshot
}

func (monitor *networkMonitor) setThroughput(downloadBps, uploadBps float64, primaryInterface string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.DownloadBps = downloadBps
	monitor.current.UploadBps = uploadBps
	monitor.current.Interface = primaryInterface
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
	appendFloatSample(&monitor.current.History.DownloadBps, downloadBps)
	appendFloatSample(&monitor.current.History.UploadBps, uploadBps)
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
		appendIntSample(&monitor.current.History.PingMs, 0)
		return
	}

	monitor.current.PingMs = pingMs
	monitor.current.Online = true
	monitor.current.Error = ""
	appendIntSample(&monitor.current.History.PingMs, pingMs)
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

func (monitor *networkMonitor) refreshWiFiRadio() {
	enabled, known, err := readWiFiRadioState()
	if err != nil {
		monitor.setWiFiState(false, false)
		return
	}

	monitor.setWiFiState(enabled, known)
}

func (monitor *networkMonitor) refreshPublicNetworkInfo() {
	publicIP, location, err := readPublicNetworkInfo()
	if err != nil {
		return
	}

	monitor.setPublicNetworkInfo(publicIP, location)
}

func (monitor *networkMonitor) setWiFiSSID(ssid string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.SSID = ssid
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
}

func (monitor *networkMonitor) setWiFiState(enabled, known bool) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.WiFiEnabled = enabled
	monitor.current.WiFiKnown = known
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
}

func (monitor *networkMonitor) setPublicNetworkInfo(publicIP, location string) {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()

	monitor.current.PublicIP = strings.TrimSpace(publicIP)
	monitor.current.Location = strings.TrimSpace(location)
	monitor.current.UpdatedAt = time.Now().Format(time.RFC3339)
}

func (monitor *networkMonitor) toggleWiFi(ctx context.Context) error {
	enabled, known, err := readWiFiRadioState()
	if err != nil {
		return err
	}
	if !known {
		return errors.New("wi-fi radio not found")
	}

	targetState := "Off"
	if !enabled {
		targetState = "On"
	}

	if err := setWiFiRadioState(ctx, targetState); err != nil {
		return err
	}

	monitor.refreshWiFiRadio()
	monitor.refreshWiFiSSID()

	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		<-timer.C
		monitor.refreshWiFiRadio()
		monitor.refreshWiFiSSID()
		monitor.refreshPublicNetworkInfo()
	}()

	return nil
}

func newNetworkHistory() networkHistory {
	return networkHistory{
		DownloadBps: make([]float64, networkHistoryLimit),
		UploadBps:   make([]float64, networkHistoryLimit),
		PingMs:      make([]int, networkHistoryLimit),
	}
}

func cloneNetworkHistory(history networkHistory) networkHistory {
	return networkHistory{
		DownloadBps: append([]float64(nil), history.DownloadBps...),
		UploadBps:   append([]float64(nil), history.UploadBps...),
		PingMs:      append([]int(nil), history.PingMs...),
	}
}

func appendFloatSample(series *[]float64, value float64) {
	sample := 0.0
	if !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 {
		sample = value
	}

	bucket := append(*series, sample)
	if len(bucket) > networkHistoryLimit {
		bucket = bucket[len(bucket)-networkHistoryLimit:]
	}

	*series = bucket
}

func appendIntSample(series *[]int, value int) {
	sample := 0
	if value > 0 {
		sample = value
	}

	bucket := append(*series, sample)
	if len(bucket) > networkHistoryLimit {
		bucket = bucket[len(bucket)-networkHistoryLimit:]
	}

	*series = bucket
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping.exe", "-n", "2", "-w", "1200", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	text := string(output)
	matches := pingTimePattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}

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

func readPublicNetworkInfo() (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipwho.is/", nil)
	if err != nil {
		return "", "", fmt.Errorf("build public ip request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("request public ip: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("public ip status %d", response.StatusCode)
	}

	var payload publicIPLookupResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", "", fmt.Errorf("decode public ip response: %w", err)
	}
	if !payload.Success {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "lookup failed"
		}
		return "", "", errors.New(message)
	}

	location := formatLocation(payload.City, payload.Region, payload.Country)
	return strings.TrimSpace(payload.IP), location, nil
}

func formatLocation(city, region, country string) string {
	parts := make([]string, 0, 2)
	if trimmedCity := strings.TrimSpace(city); trimmedCity != "" {
		parts = append(parts, trimmedCity)
	}

	countryLabel := strings.TrimSpace(country)
	if countryLabel == "" {
		countryLabel = strings.TrimSpace(region)
	}
	if countryLabel != "" {
		parts = append(parts, countryLabel)
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ", ")
}

func readWiFiRadioState() (bool, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	output, err := runHiddenPowerShell(ctx, buildWinRTRadioScript(`
$radiosTask = $asTask.MakeGenericMethod($listType).Invoke($null, @([Windows.Devices.Radios.Radio]::GetRadiosAsync()))
$radiosTask.Wait()
@($radiosTask.Result | Select-Object Name, Kind, State) | ConvertTo-Json -Compress
`))
	if err != nil {
		return false, false, err
	}

	var radios []radioSnapshot
	if err := json.Unmarshal(output, &radios); err != nil {
		return false, false, fmt.Errorf("decode radio state: %w", err)
	}

	for _, radio := range radios {
		if !isWiFiRadioKind(radio.Kind) {
			continue
		}

		return isRadioStateOn(radio.State), true, nil
	}

	return false, false, nil
}

func setWiFiRadioState(ctx context.Context, targetState string) error {
	if targetState != "On" && targetState != "Off" {
		return fmt.Errorf("unsupported wi-fi target state %q", targetState)
	}

	toggleCtx := ctx
	if toggleCtx == nil {
		toggleCtx = context.Background()
	}

	_, err := runHiddenPowerShell(toggleCtx, buildWinRTRadioScript(fmt.Sprintf(`
$accessTask = $asTask.MakeGenericMethod([Windows.Devices.Radios.RadioAccessStatus]).Invoke($null, @([Windows.Devices.Radios.Radio]::RequestAccessAsync()))
$accessTask.Wait()
if ($accessTask.Result -ne [Windows.Devices.Radios.RadioAccessStatus]::Allowed) {
  throw ("Radio access denied: " + $accessTask.Result)
}
$radiosTask = $asTask.MakeGenericMethod($listType).Invoke($null, @([Windows.Devices.Radios.Radio]::GetRadiosAsync()))
$radiosTask.Wait()
$wifiRadios = @($radiosTask.Result | Where-Object { $_.Kind -eq [Windows.Devices.Radios.RadioKind]::WiFi })
if ($wifiRadios.Count -eq 0) {
  throw 'Wi-Fi radio not found'
}
$targetState = [Windows.Devices.Radios.RadioState]::%s
foreach ($radio in $wifiRadios) {
  $setTask = $asTask.MakeGenericMethod([Windows.Devices.Radios.RadioAccessStatus]).Invoke($null, @($radio.SetStateAsync($targetState)))
  $setTask.Wait()
  if ($setTask.Result -ne [Windows.Devices.Radios.RadioAccessStatus]::Allowed) {
    throw ("Wi-Fi toggle denied: " + $setTask.Result)
  }
}
`, targetState)))
	if err != nil {
		return err
	}

	return nil
}

func isWiFiRadioKind(kind any) bool {
	switch value := kind.(type) {
	case string:
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
		return normalized == "wifi"
	case float64:
		return int(value) == 1
	case int:
		return value == 1
	default:
		return false
	}
}

func isRadioStateOn(state any) bool {
	switch value := state.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "On")
	case float64:
		return int(value) == 1
	case int:
		return value == 1
	default:
		return false
	}
}

func buildWinRTRadioScript(body string) string {
	baseScript := strings.Join([]string{
		"$ErrorActionPreference='Stop'",
		"Add-Type -AssemblyName System.Runtime.WindowsRuntime",
		"[void][Windows.Devices.Radios.Radio, Windows.Devices, ContentType=WindowsRuntime]",
		"[void][Windows.Devices.Radios.RadioAccessStatus, Windows.Devices, ContentType=WindowsRuntime]",
		"[void][Windows.Devices.Radios.RadioState, Windows.Devices, ContentType=WindowsRuntime]",
		"$asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() |",
		"  Where-Object {",
		"    $_.Name -eq 'AsTask' -and",
		"    $_.IsGenericMethodDefinition -and",
		"    $_.GetGenericArguments().Count -eq 1 -and",
		"    $_.GetParameters().Count -eq 1 -and",
		"    $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'",
		"  } |",
		"  Select-Object -First 1",
		"if (-not $asTask) {",
		"  throw 'AsTask helper not found'",
		"}",
		"$listType = [System.Collections.Generic.IReadOnlyList[Windows.Devices.Radios.Radio]]",
	}, "\n")

	return strings.TrimSpace(baseScript + "\n" + strings.TrimSpace(body))
}

func runHiddenPowerShell(ctx context.Context, script string) ([]byte, error) {
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	output, err := command.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return nil, fmt.Errorf("powershell: %w", err)
		}
		return nil, fmt.Errorf("powershell: %s", trimmed)
	}

	return output, nil
}
