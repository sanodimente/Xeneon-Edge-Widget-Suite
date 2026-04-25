//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	bridgeExeName       = "widgetbridge.exe"
	installerExeName    = "widgetbridge-installer.exe"
	installFolderName   = "WidgetBridge"
	autostartValueName  = "WidgetBridge"
	runKeyPath          = `Software\Microsoft\Windows\CurrentVersion\Run`
	defaultBridgeAddr   = "127.0.0.1:39291"
	stopPath            = "/control/stop"
	elevatedFlag        = "--elevated"
	messageTitle        = "WidgetBridge Installer"
	startupLaunchTarget = `"%s"`
	repoMainFileName    = "main.go"
	repoModuleFileName  = "go.mod"
)

func main() {
	if !hasArg(elevatedFlag) {
		if err := relaunchElevated(); err != nil {
			showError(err.Error())
		}
		return
	}

	if err := installBridge(); err != nil {
		showError(err.Error())
		os.Exit(1)
	}

	showInfo("WidgetBridge installed in C:\\Program Files\\WidgetBridge and configured to start with Windows.")
}

func installBridge() error {
	installerPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve installer path: %w", err)
	}
	installerDir := filepath.Dir(installerPath)

	sourceBridge, err := ensureSourceBridge(installerDir)
	if err != nil {
		return err
	}

	installDir, err := resolveInstallDir()
	if err != nil {
		return err
	}

	if err := stopRunningBridge(); err != nil {
		return err
	}

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install directory %s: %w", installDir, err)
	}

	installedBridgePath := filepath.Join(installDir, bridgeExeName)
	if err := copyFile(sourceBridge, installedBridgePath); err != nil {
		return err
	}

	if err := setAutostart(installedBridgePath); err != nil {
		return err
	}

	if err := launchInstalledBridge(installedBridgePath); err != nil {
		return err
	}

	return nil
}

func ensureSourceBridge(installerDir string) (string, error) {
	sourceBridge := filepath.Join(installerDir, bridgeExeName)
	info, err := os.Stat(sourceBridge)
	if err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory, expected an executable", sourceBridge)
		}

		return sourceBridge, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect %s: %w", sourceBridge, err)
	}

	if err := buildBridge(installerDir, sourceBridge); err != nil {
		return "", err
	}

	if _, err := os.Stat(sourceBridge); err != nil {
		return "", fmt.Errorf("build completed but %s is still missing: %w", sourceBridge, err)
	}

	return sourceBridge, nil
}

func buildBridge(repoDir, outputPath string) error {
	if err := ensureRepoFiles(repoDir); err != nil {
		return err
	}

	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("widgetbridge.exe is missing and Go was not found in PATH, so the installer cannot build it automatically")
	}

	cmd := exec.Command("go", "build", "-ldflags=-H=windowsgui", "-o", outputPath, ".")
	cmd.Dir = repoDir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}

		return fmt.Errorf("build %s from installer: %s", bridgeExeName, message)
	}

	return nil
}

func ensureRepoFiles(repoDir string) error {
	for _, name := range []string{repoMainFileName, repoModuleFileName} {
		path := filepath.Join(repoDir, name)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("missing %s next to %s; keep the installer inside the WidgetBridge project folder if you want it to auto-build %s", name, installerExeName, bridgeExeName)
			}

			return fmt.Errorf("inspect %s: %w", path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("expected %s to be a file", path)
		}
	}

	return nil
}

func resolveInstallDir() (string, error) {
	programFiles := strings.TrimSpace(os.Getenv("ProgramFiles"))
	if programFiles == "" {
		return "", errors.New("ProgramFiles environment variable is not available")
	}

	return filepath.Join(programFiles, installFolderName), nil
}

func stopRunningBridge() error {
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s%s", defaultBridgeAddr, stopPath), nil)
	if err != nil {
		return fmt.Errorf("build stop request: %w", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "refused") {
			return nil
		}

		return fmt.Errorf("stop running WidgetBridge: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("stop running WidgetBridge: unexpected status %s", response.Status)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", defaultBridgeAddr, 250*time.Millisecond)
		if dialErr != nil {
			return nil
		}
		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}

	return errors.New("existing WidgetBridge instance did not stop in time")
}

func copyFile(sourcePath, destinationPath string) error {
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer sourceFile.Close()

	tempPath := destinationPath + ".tmp"
	if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale temp file %s: %w", tempPath, err)
	}

	destinationFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", tempPath, err)
	}

	copyErr := copyAll(destinationFile, sourceFile)
	closeErr := destinationFile.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close %s: %w", tempPath, closeErr)
	}

	if err := os.Remove(destinationPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace %s: %w", destinationPath, err)
	}

	if err := os.Rename(tempPath, destinationPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename %s to %s: %w", tempPath, destinationPath, err)
	}

	return nil
}

func copyAll(destination io.Writer, source io.Reader) error {
	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy WidgetBridge executable: %w", err)
	}

	return nil
}

func setAutostart(bridgePath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open startup registry key: %w", err)
	}
	defer key.Close()

	if err := key.SetStringValue(autostartValueName, fmt.Sprintf(startupLaunchTarget, bridgePath)); err != nil {
		return fmt.Errorf("set startup registry value: %w", err)
	}

	return nil
}

func launchInstalledBridge(bridgePath string) error {
	cmd := exec.Command(bridgePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch installed WidgetBridge: %w", err)
	}

	return cmd.Process.Release()
}

func hasArg(expected string) bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(strings.TrimSpace(arg), expected) {
			return true
		}
	}

	return false
}

func relaunchElevated() error {
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve installer path: %w", err)
	}

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	filePtr, _ := syscall.UTF16PtrFromString(executablePath)
	argsPtr, _ := syscall.UTF16PtrFromString(elevatedFlag)

	shell32 := syscall.NewLazyDLL("shell32.dll")
	shellExecute := shell32.NewProc("ShellExecuteW")
	result, _, callErr := shellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(filePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		0,
		1,
	)

	if result <= 32 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("request administrator privileges: %w", callErr)
		}

		return fmt.Errorf("request administrator privileges failed with code %d", result)
	}

	return nil
}

func showInfo(message string) {
	showMessage(message, 0x00000040)
}

func showError(message string) {
	showMessage(message, 0x00000010)
}

func showMessage(message string, flags uintptr) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	titlePtr, _ := syscall.UTF16PtrFromString(messageTitle)
	messagePtr, _ := syscall.UTF16PtrFromString(message)
	messageBox.Call(0, uintptr(unsafe.Pointer(messagePtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
}
