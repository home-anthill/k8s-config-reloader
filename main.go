package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

// Watcher abstracts filesystem watching for testability.
type Watcher interface {
	Add(name string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

// ProcessReloader abstracts process lookup and signaling for testability.
type ProcessReloader interface {
	Reload(processName string, signal syscall.Signal) error
}

// fsnotifyWatcher adapts *fsnotify.Watcher to the Watcher interface.
type fsnotifyWatcher struct {
	w *fsnotify.Watcher
}

func (fw *fsnotifyWatcher) Add(name string) error         { return fw.w.Add(name) }
func (fw *fsnotifyWatcher) Close() error                  { return fw.w.Close() }
func (fw *fsnotifyWatcher) Events() <-chan fsnotify.Event { return fw.w.Events }
func (fw *fsnotifyWatcher) Errors() <-chan error          { return fw.w.Errors }

// osProcessReloader finds a process by name and sends a signal.
// It errors if zero or more than one process matches.
type osProcessReloader struct{}

func (r *osProcessReloader) Reload(processName string, signal syscall.Signal) error {
	processes, err := process.Processes()
	if err != nil {
		return fmt.Errorf("cannot list processes: %w", err)
	}

	var matched []*process.Process
	for _, p := range processes {
		name, err := p.Name()
		if err != nil {
			continue
		}
		if name == processName {
			matched = append(matched, p)
		}
	}

	if len(matched) == 0 {
		return fmt.Errorf("cannot find a process called %s", processName)
	}
	if len(matched) > 1 {
		pids := make([]int32, len(matched))
		for i, p := range matched {
			pids[i] = p.Pid
		}
		return fmt.Errorf("found %d processes called %s (pids: %v), expected exactly one", len(matched), processName, pids)
	}

	p := matched[0]
	if err := p.SendSignal(signal); err != nil {
		return fmt.Errorf("cannot send signal to pid %d: %w", p.Pid, err)
	}
	log.Printf("Signal %s sent to %s (pid: %d)", signal, processName, p.Pid)
	return nil
}

// Config holds the runtime configuration for the reloader.
type Config struct {
	ConfigDirs    []string
	ProcessName   string
	ReloadSignal  syscall.Signal
	DebounceDelay time.Duration
}

func run(ctx context.Context, cfg Config, w Watcher, pr ProcessReloader) error {
	defer w.Close()

	for _, dir := range cfg.ConfigDirs {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("config directory %q does not exist: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("config path %q is not a directory", dir)
		}
		if err := w.Add(dir); err != nil {
			return fmt.Errorf("failed to watch directory %q: %w", dir, err)
		}
		log.Printf("Watching directory: %s", dir)
	}

	var (
		mu            sync.Mutex
		debounceTimer *time.Timer
	)

	reload := func() {
		if err := pr.Reload(cfg.ProcessName, cfg.ReloadSignal); err != nil {
			log.Printf("Error reloading process: %v", err)
		}
	}

	go func() {
		for {
			select {
			case event, ok := <-w.Events():
				if !ok {
					return
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) {
					log.Printf("Modified file: %s", event.Name)
					mu.Lock()
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(cfg.DebounceDelay, reload)
					mu.Unlock()
				}
			case err, ok := <-w.Errors():
				if !ok {
					return
				}
				log.Printf("Watcher error: %v", err)
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
	log.Println("Received shutdown signal, exiting...")

	mu.Lock()
	if debounceTimer != nil {
		debounceTimer.Stop()
	}
	mu.Unlock()

	return nil
}

func parseConfig() (Config, error) {
	configDirEnv := os.Getenv("CONFIG_DIR")
	if configDirEnv == "" {
		return Config{}, errors.New("CONFIG_DIR env var is missing")
	}

	processNameEnv := os.Getenv("PROCESS_NAME")
	if processNameEnv == "" {
		return Config{}, errors.New("PROCESS_NAME env var is missing")
	}

	var reloadSignal syscall.Signal
	if reloadSignalEnv := os.Getenv("RELOAD_SIGNAL"); reloadSignalEnv == "" {
		log.Print("RELOAD_SIGNAL env var is missing, using default SIGHUP")
		reloadSignal = syscall.SIGHUP
	} else {
		reloadSignal = unix.SignalNum(reloadSignalEnv)
		if reloadSignal == 0 {
			return Config{}, fmt.Errorf("unknown signal for RELOAD_SIGNAL: %s", reloadSignalEnv)
		}
	}

	var dirs []string
	for d := range strings.SplitSeq(configDirEnv, ",") {
		d = strings.TrimSpace(d)
		if d != "" {
			dirs = append(dirs, d)
		}
	}

	return Config{
		ConfigDirs:    dirs,
		ProcessName:   processNameEnv,
		ReloadSignal:  reloadSignal,
		DebounceDelay: 500 * time.Millisecond,
	}, nil
}

// to try to run this program locally use:
//
//	PROCESS_NAME=producer CONFIG_DIR=./config-folder make run
//
// Be sure to run also 'producer' or another process in background
func main() {
	cfg, err := parseConfig()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting with CONFIG_DIR=%s, PROCESS_NAME=%s, RELOAD_SIGNAL=%s",
		strings.Join(cfg.ConfigDirs, ","), cfg.ProcessName, cfg.ReloadSignal)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Cannot create watcher: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, cfg, &fsnotifyWatcher{w: fsw}, &osProcessReloader{}); err != nil {
		log.Fatal(err)
	}
}
