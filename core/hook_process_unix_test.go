//go:build !windows && !linux

package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func hookFixtureExited(process *os.Process) bool {
	if errors.Is(process.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-o", "stat=", "-p", strconv.Itoa(process.Pid)).Output()
	if err != nil {
		return errors.Is(process.Signal(syscall.Signal(0)), os.ErrProcessDone)
	}
	state := strings.TrimSpace(string(output))
	return strings.HasPrefix(state, "Z") || strings.HasPrefix(state, "X")
}

func findRunningHookFixtureProcesses() ([]*os.Process, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	var processes []*os.Process
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		pidText, command, ok := strings.Cut(line, " ")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil || pid == os.Getpid() {
			continue
		}
		command, err = filepath.EvalSymlinks(strings.TrimSpace(command))
		if err != nil || command != executable {
			continue
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		processes = append(processes, process)
	}
	return processes, nil
}
