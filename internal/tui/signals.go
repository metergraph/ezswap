package tui

import (
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// ForwardSignals sends SIGINT and SIGTERM to the program as a SignalMsg
// until the returned function is called.
func ForwardSignals(p *tea.Program) func() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				name := "SIGINT"
				if s == syscall.SIGTERM {
					name = "SIGTERM"
				}
				p.Send(SignalMsg{Name: name})
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
