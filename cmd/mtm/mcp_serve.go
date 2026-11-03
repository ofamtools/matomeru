package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/muesli/cancelreader"
	catalogmcp "ofam.dev/ochazuke/cli/mcp"
)

func (a *App) serveMCP(ctx context.Context, server *catalogmcp.Server, stdin io.Reader, stdout, stderr io.Writer) (err error) {
	var remove func() error
	registered := make(chan struct{})
	// Signal handling belongs only to the real stdio process, never an embedded
	// server or an in-process test. Install before creating even a partial entry.
	if stdin == os.Stdin {
		stop := mcpTermination(func() error {
			<-registered
			if remove != nil {
				return remove()
			}
			return nil
		}, stderr)
		defer stop()
	}
	remove, err = registerMCP(a.dataPath, a.configDir)
	close(registered)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, remove()) }()
	// Cancellation must retire the record even if a tool or stdout is blocked.
	cancelled := make(chan struct{})
	stopCleanup := context.AfterFunc(ctx, func() {
		defer close(cancelled)
		_ = remove() // The deferred call reports the same memoized cleanup error.
	})
	defer func() {
		if !stopCleanup() {
			<-cancelled
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}

	in, closeInput, err := mcpCancellableInput(ctx, stdin)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeInput()) }()
	err = server.Serve(ctx, in, stdout)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// The shared scanner checks context between requests. Wake its blocked read
// without running the headless runtime concurrently or closing process stdin.
func mcpCancellableInput(ctx context.Context, in io.Reader) (io.Reader, func() error, error) {
	var cancel func()
	closeReader := func() error { return nil }
	reader := in
	if file, ok := in.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return nil, nil, err
		}
		// Regular files and /dev/null have immediate reads; Linux epoll cannot
		// register them. Real client stdio pipes use the cancellable OS reader.
		if info.Mode()&os.ModeNamedPipe != 0 {
			r, err := cancelreader.NewReader(in)
			if err != nil {
				return nil, nil, err
			}
			reader, closeReader = r, r.Close
			cancel = func() { r.Cancel() }
		}
	} else if closer, ok := in.(io.Closer); ok {
		cancel = func() { _ = closer.Close() }
	}
	if cancel == nil {
		return reader, closeReader, nil
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		cancel()
	})
	return reader, func() error {
		if !stop() {
			<-done
		}
		return closeReader()
	}, nil
}

func mcpTermination(remove func() error, stderr io.Writer) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var sig os.Signal
		select {
		case sig = <-signals:
		case <-stop:
			// Stop disables notifications before closing stop. Drain a signal
			// already received so an EOF/signal race cannot swallow termination.
			select {
			case sig = <-signals:
			default:
				return
			}
		}
		if err := remove(); err != nil {
			fmt.Fprintln(stderr, "mtm: MCP registry cleanup:", err)
		}
		// Preserve termination by the original signal, including its status.
		signal.Stop(signals)
		process, err := os.FindProcess(os.Getpid())
		if err == nil {
			err = process.Signal(sig)
		}
		if err != nil {
			fmt.Fprintln(stderr, "mtm: re-raise termination signal:", err)
			os.Exit(1)
		}
	}()
	return func() {
		signal.Stop(signals)
		close(stop)
		<-done
	}
}
