package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"
)

func lockServiceFile(path string) (func(), error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// A zero sharing mode gives this process exclusive access until CloseHandle
	// or process exit; the persistent empty file is harmless after a crash.
	// https://learn.microsoft.com/en-us/windows/win32/fileio/creating-and-opening-files
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, syscall.Errno(32)) { // ERROR_SHARING_VIOLATION
			return nil, errServiceLocked
		}
		return nil, err
	}
	return func() { syscall.CloseHandle(h) }, nil
}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return kill.Run()
	}
}
func killDescendants(cmd *exec.Cmd) {}
func backgroundProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200}
}

func showStartupError(message string) {
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("L-Bridge could not start")
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

func offerRestart(running string) bool {
	message := "L-Bridge " + running + " is still running in the background. Closing its browser tab does not stop it.\n\nRestart with version " + version + " now?\n\nThis stops the current bridge and its active work, while keeping saved goals and files. Choose No to reopen the running version."
	text, _ := syscall.UTF16PtrFromString(message)
	title, _ := syscall.UTF16PtrFromString("Update the running L-Bridge")
	result, _, _ := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x04|0x20|0x100)
	return result == 6
}

func openSetupWindow(encoded string) error {
	operation, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	args, _ := syscall.UTF16PtrFromString("-NoProfile -EncodedCommand " + encoded)
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(operation)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(args)), 0, 1)
	if result <= 32 {
		return fmt.Errorf("Windows could not launch PowerShell (code %d)", result)
	}
	return nil
}
