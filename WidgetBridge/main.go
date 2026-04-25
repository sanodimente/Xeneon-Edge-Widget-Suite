package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
)

const (
	defaultAddr             = "127.0.0.1:39291"
	stopPath                = "/control/stop"
	networkStatusPath       = "/network/status"
	singleInstanceMutexName = "Local\\XeneonWidgets.WidgetBridge"
)

type actionRequest struct {
	Source string `json:"source"`
	Label  string `json:"label"`
}

type responseBody struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type actionDefinition struct {
	Message string
	Run     func(context.Context) error
}

type widgetDefinition struct {
	ID   string
	Name string
}

type bridgeApp struct {
	addr    string
	actions map[string]actionDefinition
	widgets []widgetDefinition
	network *networkMonitor

	server *http.Server

	mu        sync.RWMutex
	enabled   bool
	lastError string

	shutdownOnce sync.Once

	statusItem *systray.MenuItem
	toggleItem *systray.MenuItem
	quitItem   *systray.MenuItem
}

func main() {
	stopOnly := flag.Bool("stop", false, "stop the running Widget Bridge instance")
	flag.Parse()

	addr := getenvDefault("WIDGET_BRIDGE_ADDR", defaultAddr)
	if *stopOnly {
		if err := requestStop(addr); err != nil {
			log.Fatal(err)
		}

		log.Printf("Stop signal sent to http://%s", addr)
		return
	}

	instanceLock, alreadyRunning, err := acquireSingleInstanceLock(singleInstanceMutexName)
	if err != nil {
		log.Fatal(err)
	}
	if alreadyRunning {
		return
	}
	defer releaseSingleInstanceLock(instanceLock)

	app := newBridgeApp(addr)
	hideConsoleWindowIfStandalone()
	systray.Run(app.onReady, app.onExit)
}

func newBridgeApp(addr string) *bridgeApp {
	app := &bridgeApp{
		addr:    addr,
		actions: buildActionDefinitions(),
		widgets: []widgetDefinition{
			{ID: "power-pad", Name: "Power Pad"},
			{ID: "net-pulse", Name: "Net Pulse"},
		},
		network: newNetworkMonitor(getenvDefault("WIDGET_BRIDGE_PING_TARGET", "1.1.1.1")),
		enabled: true,
	}

	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mux.HandleFunc("/health", withCORS(app.handleHealth))
	mux.HandleFunc(stopPath, withCORS(app.handleStop))
	mux.HandleFunc(networkStatusPath, withCORS(app.handleNetworkStatus))
	mux.HandleFunc("/action/", withCORS(app.handleAction))

	server.Handler = mux
	app.server = server

	return app
}

func buildActionDefinitions() map[string]actionDefinition {
	return map[string]actionDefinition{
		"lock": {
			Message: "Workstation locked",
			Run: func(ctx context.Context) error {
				return startDetached(ctx, "rundll32.exe", "user32.dll,LockWorkStation")
			},
		},
		"sleep": {
			Message: "Sleep requested",
			Run: func(ctx context.Context) error {
				return startDetached(ctx, "rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0")
			},
		},
		"restart": {
			Message: "Restart requested",
			Run: func(ctx context.Context) error {
				return startDetached(ctx, "shutdown.exe", "/r", "/t", "0")
			},
		},
		"shutdown": {
			Message: "Shutdown requested",
			Run: func(ctx context.Context) error {
				return startDetached(ctx, "shutdown.exe", "/s", "/t", "0")
			},
		},
		"task-manager": {
			Message: "Task Manager opened",
			Run: func(ctx context.Context) error {
				return startDetachedVisible(ctx, "cmd.exe", "/c", "start", "", "taskmgr.exe")
			},
		},
		"power-settings": {
			Message: "Power Settings opened",
			Run: func(ctx context.Context) error {
				return startDetached(ctx, "cmd.exe", "/c", "start", "", "ms-settings:powersleep")
			},
		},
	}
}

func (app *bridgeApp) onReady() {
	systray.SetTitle("WidgetBridge")
	systray.SetTooltip(app.tooltipText())
	systray.SetIcon(trayIconData())

	app.statusItem = systray.AddMenuItem("Bridge: starting", "Current WidgetBridge status")
	app.statusItem.Disable()
	app.toggleItem = systray.AddMenuItem("Disable bridge", "Enable or disable the local WidgetBridge HTTP API")

	systray.AddSeparator()
	widgetsHeader := systray.AddMenuItem("Available widgets", "Widgets that use this bridge")
	widgetsHeader.Disable()
	for _, widget := range app.widgets {
		item := systray.AddMenuItem(widget.Name, widget.ID)
		item.Disable()
	}

	systray.AddSeparator()
	endpointItem := systray.AddMenuItem(fmt.Sprintf("Endpoint: http://%s", app.addr), "Current WidgetBridge address")
	endpointItem.Disable()
	app.quitItem = systray.AddMenuItem("Exit WidgetBridge", "Close the tray app and stop the local bridge")

	app.syncMenuState()

	go app.listenForMenuClicks()
	go app.serve()
}

func (app *bridgeApp) onExit() {
	if app.network != nil {
		app.network.stop()
	}

	app.shutdownServer()
}

func (app *bridgeApp) listenForMenuClicks() {
	for {
		select {
		case <-app.toggleItem.ClickedCh:
			app.setEnabled(!app.isEnabled())
		case <-app.quitItem.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (app *bridgeApp) serve() {
	log.Printf("Widget bridge listening on http://%s", app.addr)
	if err := app.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("listen err=%v", err)
		app.setLastError(err.Error())
		return
	}

	log.Printf("Widget bridge stopped")
}

func (app *bridgeApp) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{
			OK:    false,
			Error: "method not allowed",
		})
		return
	}

	if !app.isEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, responseBody{
			OK:    false,
			Error: "widget bridge disabled from tray",
		})
		return
	}

	writeJSON(w, http.StatusOK, responseBody{
		OK:      true,
		Message: "Widget bridge connected",
	})
}

func (app *bridgeApp) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{
			OK:    false,
			Error: "method not allowed",
		})
		return
	}

	writeJSON(w, http.StatusOK, responseBody{
		OK:      true,
		Message: "Widget bridge stopping",
	})

	go systray.Quit()
}

func (app *bridgeApp) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{
			OK:    false,
			Error: "method not allowed",
		})
		return
	}

	if !app.isEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, responseBody{
			OK:    false,
			Error: "widget bridge disabled from tray",
		})
		return
	}

	snapshot := app.network.snapshot()
	writeJSONValue(w, http.StatusOK, networkStatusResponse{
		OK:      true,
		Message: "Network metrics ready",
		Metrics: snapshot,
	})
}

func (app *bridgeApp) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{
			OK:    false,
			Error: "method not allowed",
		})
		return
	}

	actionName := strings.TrimPrefix(r.URL.Path, "/action/")
	if actionName == "" || strings.Contains(actionName, "/") {
		writeJSON(w, http.StatusNotFound, responseBody{
			OK:    false,
			Error: "unknown action",
		})
		return
	}

	action, ok := app.actions[actionName]
	if !ok {
		writeJSON(w, http.StatusNotFound, responseBody{
			OK:    false,
			Error: "unknown action",
		})
		return
	}

	if !app.isEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, responseBody{
			OK:    false,
			Error: "widget bridge disabled from tray",
		})
		return
	}

	var req actionRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, responseBody{
				OK:    false,
				Error: "invalid json body",
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := action.Run(ctx); err != nil {
		log.Printf("action=%s source=%s label=%s err=%v", actionName, req.Source, req.Label, err)
		writeJSON(w, http.StatusInternalServerError, responseBody{
			OK:    false,
			Error: err.Error(),
		})
		return
	}

	log.Printf("action=%s source=%s label=%s", actionName, req.Source, req.Label)
	writeJSON(w, http.StatusOK, responseBody{
		OK:      true,
		Message: action.Message,
	})
}

func (app *bridgeApp) shutdownServer() {
	app.shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := app.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("shutdown err=%v", err)
		}
	})
}

func (app *bridgeApp) isEnabled() bool {
	app.mu.RLock()
	defer app.mu.RUnlock()

	return app.enabled
}

func (app *bridgeApp) setEnabled(enabled bool) {
	app.mu.Lock()
	app.enabled = enabled
	app.mu.Unlock()

	app.syncMenuState()
}

func (app *bridgeApp) setLastError(message string) {
	app.mu.Lock()
	app.lastError = strings.TrimSpace(message)
	app.mu.Unlock()

	app.syncMenuState()
}

func (app *bridgeApp) snapshotState() (bool, string) {
	app.mu.RLock()
	defer app.mu.RUnlock()

	return app.enabled, app.lastError
}

func (app *bridgeApp) syncMenuState() {
	if app.statusItem == nil || app.toggleItem == nil {
		return
	}

	enabled, lastError := app.snapshotState()
	if lastError != "" {
		app.statusItem.SetTitle("Bridge error: " + shortenText(lastError, 48))
		app.toggleItem.SetTitle("Bridge unavailable")
		app.toggleItem.Disable()
		systray.SetTooltip("WidgetBridge error - " + shortenText(lastError, 64))
		return
	}

	if enabled {
		app.statusItem.SetTitle("Bridge: ON")
		app.toggleItem.SetTitle("Disable bridge")
		app.toggleItem.Enable()
		systray.SetTooltip(app.tooltipText())
		return
	}

	app.statusItem.SetTitle("Bridge: OFF")
	app.toggleItem.SetTitle("Enable bridge")
	app.toggleItem.Enable()
	systray.SetTooltip("WidgetBridge OFF - actions disabled from tray")
}

func (app *bridgeApp) tooltipText() string {
	if app.isEnabled() {
		return fmt.Sprintf("WidgetBridge ON - http://%s", app.addr)
	}

	return "WidgetBridge OFF - actions disabled from tray"
}

func shortenText(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= max {
		return trimmed
	}

	if max <= 3 {
		return trimmed[:max]
	}

	return trimmed[:max-3] + "..."
}

func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body responseBody) {
	writeJSONValue(w, status, body)
}

func writeJSONValue(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write json: %v", err)
	}
}

func startDetached(_ context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008, // DETACHED_PROCESS
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	return cmd.Process.Release()
}

func startDetachedVisible(_ context.Context, name string, args ...string) error {
	cmd := exec.Command(name, args...)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	return cmd.Process.Release()
}

func requestStop(addr string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s%s", addr, stopPath), nil)
	if err != nil {
		return fmt.Errorf("build stop request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("stop running bridge at http://%s: %w", addr, err)
	}
	defer response.Body.Close()

	var payload responseBody
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode stop response: %w", err)
	}

	if response.StatusCode != http.StatusOK || !payload.OK {
		message := payload.Error
		if message == "" {
			message = response.Status
		}

		return fmt.Errorf("stop running bridge at http://%s: %s", addr, message)
	}

	return nil
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func hideConsoleWindowIfStandalone() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	user32 := syscall.NewLazyDLL("user32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	getConsoleProcessList := kernel32.NewProc("GetConsoleProcessList")
	showWindow := user32.NewProc("ShowWindow")

	processList := make([]uint32, 2)
	count, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&processList[0])), uintptr(len(processList)))
	if count != 1 {
		return
	}

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return
	}

	const swHide = 0
	showWindow.Call(hwnd, uintptr(swHide))
}

func acquireSingleInstanceLock(name string) (syscall.Handle, bool, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")

	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, fmt.Errorf("encode mutex name: %w", err)
	}

	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		if callErr != syscall.Errno(0) {
			return 0, false, fmt.Errorf("create mutex: %w", callErr)
		}

		return 0, false, errors.New("create mutex: unknown error")
	}

	const errorAlreadyExists syscall.Errno = 183
	if errors.Is(callErr, errorAlreadyExists) {
		return syscall.Handle(handle), true, nil
	}

	return syscall.Handle(handle), false, nil
}

func releaseSingleInstanceLock(handle syscall.Handle) {
	if handle == 0 {
		return
	}

	if err := syscall.CloseHandle(handle); err != nil {
		log.Printf("close mutex handle: %v", err)
	}
}
