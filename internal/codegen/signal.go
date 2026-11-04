package codegen

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// removeOnSignal removes dir when the process gets SIGINT or SIGTERM before
// the returned function is called, and then dies of the signal, as it would
// have without the handler: a deferred removal does not run when a signal
// ends the process. A signal the process ignores stays ignored.
func removeOnSignal(dir string) (stop func()) {
	var sigs []os.Signal
	for _, s := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !signal.Ignored(s) {
			sigs = append(sigs, s)
		}
	}
	if len(sigs) == 0 {
		return func() {}
	}
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, sigs...)
	go func() {
		select {
		case s := <-ch:
			_ = os.RemoveAll(dir)
			signal.Stop(ch)
			reraise(s)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// reraise ends the process with the signal s, whose handler is gone, or
// with exit code 130 where it cannot.
func reraise(s os.Signal) {
	if p, err := os.FindProcess(os.Getpid()); err == nil && p.Signal(s) == nil {
		time.Sleep(time.Second) // the signal's default action ends the process
	}
	os.Exit(130)
}
