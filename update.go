package main

import (
	_ "embed"
	"errors"
	"os"
	"runtime"
)

const releaseRepository = "bytexv/L-Bridge"

//go:embed update.ps1
var updateProgram string

func updateScript(target, dir string) string {
	return "$target=" + psLiteral(target) + "; $dataDir=" + psLiteral(dir) + "; $currentVersion=" + psLiteral(version) + "; $repository=" + psLiteral(releaseRepository) + ";\n" + updateProgram
}
func launchUpdate(dir string) error {
	if runtime.GOOS != "windows" {
		return errors.New("the automatic updater is for Windows; download builds from GitHub Releases")
	}
	target, err := os.Executable()
	if err != nil {
		return err
	}
	return openSetupWindow(encodePowerShell(updateScript(target, dir)))
}
func activeCommands() bool { jobs.Lock(); defer jobs.Unlock(); return len(jobs.items) > 0 }
