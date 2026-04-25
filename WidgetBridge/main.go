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
	"syscall"
	"time"
)

const (
	defaultAddr = "127.0.0.1:39291"
	stopPath    = "/control/stop"
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

	actions := map[string]actionDefinition{
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

	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
	}

	mux.HandleFunc("/health", withCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, responseBody{
				OK:    false,
				Error: "method not allowed",
			})
			return
		}

		writeJSON(w, http.StatusOK, responseBody{
			OK:      true,
			Message: "Widget bridge connected",
		})
	}))

	mux.HandleFunc(stopPath, withCORS(func(w http.ResponseWriter, r *http.Request) {
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

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("shutdown err=%v", err)
			}
		}()
	}))

	mux.HandleFunc("/action/", withCORS(func(w http.ResponseWriter, r *http.Request) {
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

		action, ok := actions[actionName]
		if !ok {
			writeJSON(w, http.StatusNotFound, responseBody{
				OK:    false,
				Error: "unknown action",
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
	}))

	server.Handler = mux

	log.Printf("Widget bridge listening on http://%s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}

	log.Printf("Widget bridge stopped")
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
