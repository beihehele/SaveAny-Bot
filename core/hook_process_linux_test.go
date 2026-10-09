package core

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func hookFixtureExited(process *os.Process) bool {
	if errors.Is(process.Signal(syscall.Signal(0)), os.ErrProcessDone) {
		return true
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(process.Pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	// A killed grandchild may remain a zombie until PID 1 reaps it. Its files
	// are closed and it cannot run; signal 0 alone would misreport it as alive.
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return false
	}
	state := strings.Fields(string(data)[end+2:])
	return len(state) > 0 && (state[0] == "Z" || state[0] == "X")
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
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var processes []*os.Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		image, err := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if err != nil || image != executable {
			if err := process.Release(); err != nil {
				return processes, err
			}
			continue
		}
		processes = append(processes, process)
	}
	return processes, nil
}
