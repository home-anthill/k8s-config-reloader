package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// --- Fakes ---

type fakeWatcher struct {
	events chan fsnotify.Event
	errors chan error
	dirs   []string
	closed bool
	addErr error
	once   sync.Once
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{
		events: make(chan fsnotify.Event, 10),
		errors: make(chan error, 10),
	}
}

func (w *fakeWatcher) Add(name string) error {
	if w.addErr != nil {
		return w.addErr
	}
	w.dirs = append(w.dirs, name)
	return nil
}

func (w *fakeWatcher) Close() error {
	w.once.Do(func() {
		w.closed = true
		close(w.events)
		close(w.errors)
	})
	return nil
}

func (w *fakeWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *fakeWatcher) Errors() <-chan error          { return w.errors }

type reloadCall struct {
	processName string
	signal      syscall.Signal
}

type fakeProcessReloader struct {
	mu    sync.Mutex
	calls []reloadCall
	err   error
}

func (r *fakeProcessReloader) Reload(processName string, signal syscall.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, reloadCall{processName, signal})
	return r.err
}

func (r *fakeProcessReloader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *fakeProcessReloader) lastCall() reloadCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

// --- Helpers ---

func defaultConfig(dirs []string) Config {
	return Config{
		ConfigDirs:    dirs,
		ProcessName:   "myprocess",
		ReloadSignal:  syscall.SIGHUP,
		DebounceDelay: 50 * time.Millisecond,
	}
}

var _ = Describe("Main", func() {

	Describe("Config validation", func() {
		It("should return an error for a non-existent directory", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cfg := defaultConfig([]string{"/nonexistent/path/that/does/not/exist"})
			w := newFakeWatcher()
			pr := &fakeProcessReloader{}

			err := run(ctx, cfg, w, pr)
			Expect(err).To(HaveOccurred())
		})

		It("should return an error when config path is a file, not a directory", func() {
			dir := GinkgoT().TempDir()
			filePath := filepath.Join(dir, "notadir.txt")
			Expect(os.WriteFile(filePath, []byte("hello"), 0644)).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cfg := defaultConfig([]string{filePath})
			w := newFakeWatcher()
			pr := &fakeProcessReloader{}

			err := run(ctx, cfg, w, pr)
			Expect(err).To(HaveOccurred())
		})

		It("should watch valid config directories", func() {
			dir1 := GinkgoT().TempDir()
			dir2 := GinkgoT().TempDir()

			ctx, cancel := context.WithCancel(context.Background())
			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir1, dir2})

			go func() {
				defer GinkgoRecover()
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			err := run(ctx, cfg, w, pr)
			Expect(err).NotTo(HaveOccurred())
			Expect(w.dirs).To(Equal([]string{dir1, dir2}))
		})
	})

	Describe("Event handling", func() {
		It("should trigger a reload on Write events", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Write}
			}()

			go func() {
				defer GinkgoRecover()
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(Equal(1))
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(1))

			call := pr.lastCall()
			Expect(call.processName).To(Equal("myprocess"))
			Expect(call.signal).To(Equal(syscall.SIGHUP))
		})

		It("should trigger a reload on Remove events", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Remove}
			}()

			go func() {
				defer GinkgoRecover()
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(Equal(1))
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(1))
		})

		It("should not trigger a reload on Create events", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Create}
				time.Sleep(150 * time.Millisecond)
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(0))
		})

		It("should not trigger a reload on Chmod events", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Chmod}
				time.Sleep(150 * time.Millisecond)
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(0))
		})

		It("should not crash when the reloader returns an error", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{err: fmt.Errorf("process not found")}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Write}
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(Equal(1))
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(1))
		})
	})

	Describe("Debounce", func() {
		It("should coalesce multiple rapid events into a single reload", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				for i := range 5 {
					w.events <- fsnotify.Event{Name: fmt.Sprintf("file%d.conf", i), Op: fsnotify.Write}
				}
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(BeNumerically(">=", 1))
				// extra wait to ensure no additional calls
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(1))
		})

		It("should trigger separate reloads for events spaced apart", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.events <- fsnotify.Event{Name: "file1.conf", Op: fsnotify.Write}
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(BeNumerically(">=", 1))

				w.events <- fsnotify.Event{Name: "file2.conf", Op: fsnotify.Write}
				Eventually(pr.callCount, 500*time.Millisecond, 5*time.Millisecond).Should(BeNumerically(">=", 2))

				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
			Expect(pr.callCount()).To(Equal(2))
		})
	})

	Describe("Graceful shutdown", func() {
		It("should clean up on context cancellation", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			done := make(chan error, 1)
			go func() {
				done <- run(ctx, cfg, w, pr)
			}()

			time.Sleep(50 * time.Millisecond)
			cancel()

			Eventually(done, 2*time.Second).Should(Receive(BeNil()))
			Expect(w.closed).To(BeTrue())
		})

		It("should clean up on context timeout", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			done := make(chan error, 1)
			go func() {
				done <- run(ctx, cfg, w, pr)
			}()

			Eventually(done, 2*time.Second).Should(Receive(BeNil()))
			Expect(w.closed).To(BeTrue())
		})
	})

	Describe("Watcher errors", func() {
		It("should not return an error when the watcher reports errors", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			go func() {
				defer GinkgoRecover()
				time.Sleep(20 * time.Millisecond)
				w.errors <- fmt.Errorf("disk error")
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
		})

		It("should return an error when watcher.Add fails", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := newFakeWatcher()
			w.addErr = fmt.Errorf("permission denied")
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			err := run(ctx, cfg, w, pr)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Edge cases", func() {
		It("should handle empty config dirs", func() {
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{})

			go func() {
				defer GinkgoRecover()
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			Expect(run(ctx, cfg, w, pr)).To(Succeed())
		})

		It("should stop the debounce timer on shutdown before it fires", func() {
			dir := GinkgoT().TempDir()
			ctx, cancel := context.WithCancel(context.Background())

			w := newFakeWatcher()
			pr := &fakeProcessReloader{}
			cfg := defaultConfig([]string{dir})

			done := make(chan error, 1)
			go func() {
				done <- run(ctx, cfg, w, pr)
			}()

			// send event then immediately cancel before debounce fires
			time.Sleep(20 * time.Millisecond)
			w.events <- fsnotify.Event{Name: "test.conf", Op: fsnotify.Write}
			time.Sleep(5 * time.Millisecond)
			cancel()

			Eventually(done, 2*time.Second).Should(Receive(BeNil()))

			// wait past debounce duration to ensure timer was stopped
			time.Sleep(100 * time.Millisecond)
			Expect(pr.callCount()).To(Equal(0))
		})
	})
})

var _ = Describe("parseConfig", func() {
	It("should return an error when CONFIG_DIR is missing", func() {
		GinkgoT().Setenv("CONFIG_DIR", "")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")

		_, err := parseConfig()
		Expect(err).To(MatchError(ContainSubstring("CONFIG_DIR")))
	})

	It("should return an error when PROCESS_NAME is missing", func() {
		GinkgoT().Setenv("CONFIG_DIR", "/tmp")
		GinkgoT().Setenv("PROCESS_NAME", "")

		_, err := parseConfig()
		Expect(err).To(MatchError(ContainSubstring("PROCESS_NAME")))
	})

	It("should return an error for an unknown RELOAD_SIGNAL", func() {
		GinkgoT().Setenv("CONFIG_DIR", "/tmp")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")
		GinkgoT().Setenv("RELOAD_SIGNAL", "INVALID_SIGNAL")

		_, err := parseConfig()
		Expect(err).To(MatchError(ContainSubstring("unknown signal")))
	})

	It("should default to SIGHUP when RELOAD_SIGNAL is not set", func() {
		GinkgoT().Setenv("CONFIG_DIR", "/tmp")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")
		GinkgoT().Setenv("RELOAD_SIGNAL", "")

		cfg, err := parseConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ReloadSignal).To(Equal(syscall.SIGHUP))
	})

	It("should parse a valid RELOAD_SIGNAL", func() {
		GinkgoT().Setenv("CONFIG_DIR", "/tmp")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")
		GinkgoT().Setenv("RELOAD_SIGNAL", "SIGUSR1")

		cfg, err := parseConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ReloadSignal).To(Equal(syscall.SIGUSR1))
	})

	It("should parse comma-separated CONFIG_DIR with spaces", func() {
		GinkgoT().Setenv("CONFIG_DIR", " /tmp/a , /tmp/b , ")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")
		GinkgoT().Setenv("RELOAD_SIGNAL", "")

		cfg, err := parseConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ConfigDirs).To(Equal([]string{"/tmp/a", "/tmp/b"}))
	})

	It("should set the debounce delay to 500ms", func() {
		GinkgoT().Setenv("CONFIG_DIR", "/tmp")
		GinkgoT().Setenv("PROCESS_NAME", "myprocess")
		GinkgoT().Setenv("RELOAD_SIGNAL", "")

		cfg, err := parseConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DebounceDelay).To(Equal(500 * time.Millisecond))
	})
})

var _ = Describe("osProcessReloader", func() {
	var r *osProcessReloader

	BeforeEach(func() {
		r = &osProcessReloader{}
	})

	It("should return an error when no process matches the name", func() {
		err := r.Reload("nonexistent_process_xyz_12345", syscall.SIGHUP)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot find a process called nonexistent_process_xyz_12345"))
	})

	It("should send a signal to a matching process", func() {
		cmd := exec.Command("sleep", "120")
		Expect(cmd.Start()).To(Succeed())
		DeferCleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

		err := r.Reload("sleep", syscall.SIGUSR1)
		if err != nil && strings.Contains(err.Error(), "expected exactly one") {
			Skip("multiple sleep processes running, skipping")
		}
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return an error when multiple processes match the name", func() {
		cmd1 := exec.Command("sleep", "120")
		Expect(cmd1.Start()).To(Succeed())
		DeferCleanup(func() { _ = cmd1.Process.Kill(); _ = cmd1.Wait() })

		cmd2 := exec.Command("sleep", "120")
		Expect(cmd2.Start()).To(Succeed())
		DeferCleanup(func() { _ = cmd2.Process.Kill(); _ = cmd2.Wait() })

		err := r.Reload("sleep", syscall.SIGUSR1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected exactly one"))
	})
})
