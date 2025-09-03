package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

// to try to run this program locally use:
// 	make run -- PROCESS_NAME=producer CONFIG_DIR=./config-folder VERBOSE
// Be sure to run also 'producer' or another process in background

func main() {
	configDirEnv := os.Getenv("CONFIG_DIR")
	if configDirEnv == "" {
		log.Fatal("CONFIG_DIR env var is missing, exiting...")
	}

	processNameEnv := os.Getenv("PROCESS_NAME")
	if processNameEnv == "" {
		log.Fatal("PROCESS_NAME env var is missing, exiting...")
	}

	reloadSignalEnv := os.Getenv("RELOAD_SIGNAL")
	var reloadSignal syscall.Signal
	if reloadSignalEnv == "" {
		log.Printf("RELOAD_SIGNAL env var is missing, using default SIGHUP")
		reloadSignal = syscall.SIGHUP
	} else {
		reloadSignal = unix.SignalNum(reloadSignalEnv)
		if reloadSignal == 0 {
			log.Fatalf("Unknown signal for RELOAD_SIGNAL: %s", reloadSignalEnv)
		}
	}

	log.Printf("Starting with CONFIG_DIR=%s, PROCESS_NAME=%s, RELOAD_SIGNAL=%s\n", configDirEnv, processNameEnv, reloadSignal)

	// Create new watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// Start listening for events
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				log.Println("Event:", event)
				if event.Has(fsnotify.Write) {
					// if event.Op&fsnotify.Chmod != fsnotify.Chmod {
					log.Println("Modified file:", event.Name)
					err1 := reloadProcess(processNameEnv, reloadSignal)
					if err1 != nil {
						log.Println("Error:", err1)
					}
				}
			case err2, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("Error:", err2)
			}
		}
	}()

	// Add a path
	configDirs := strings.Split(configDirEnv, ",")
	for _, dir := range configDirs {
		err = watcher.Add(dir)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Block main goroutine forever
	<-make(chan struct{})
}

func reloadProcess(processName string, signal syscall.Signal) error {
	log.Println("Reloading processName:", processName)
	proc, err := findProcess(processName)
	if err != nil {
		return err
	}
	log.Println("Reloading process with pid:", proc.Pid)
	err = proc.SendSignal(signal)
	if err != nil {
		return fmt.Errorf("cannot send signal: %v", err)
	}

	log.Printf("Signal %s sent to %s (pid: %d)\n", signal, processName, proc.Pid)
	return nil
}

func findProcess(processName string) (*process.Process, error) {
	log.Printf("Searching for process called = %s\n", processName)
	processes, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("cannot list processes: %v", err)
	}

	for _, p := range processes {
		n, _ := p.Name()
		if n == processName {
			log.Printf("Process %s (pid: %d) found\n", n, p.Pid)
			return p, nil
		}
	}

	return nil, fmt.Errorf("cannot find a process called %s", processName)
}
