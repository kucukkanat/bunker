package mcp

import (
	"bufio"
	"context"
	"io"
	"sync"
)

// ServeStdio runs the stdio transport: newline-delimited JSON-RPC on r/w.
// Requests are handled concurrently (a slow tool call doesn't block ping)
// and replies are written one per line. It returns when r is exhausted
// and all in-flight requests are answered.
func ServeStdio(ctx context.Context, s *Server, r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 1<<20)
	var wmu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	write := func(b []byte) {
		wmu.Lock()
		defer wmu.Unlock()
		w.Write(append(b, '\n'))
		if f, ok := w.(interface{ Flush() error }); ok {
			f.Flush()
		}
	}
	var rerr error
	for {
		line, err := br.ReadBytes('\n')
		if len(trimSpace(line)) > 0 {
			l := append([]byte(nil), line...)
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				if out := s.HandleRaw(ctx, l); out != nil {
					write(out)
				}
			}()
		}
		if err != nil {
			if err != io.EOF {
				rerr = err
			}
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	wg.Wait()
	return rerr
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return b
}
