package fnrt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/sys"
)

const (
	// Protocol lines carry host requests (uploads, fetch bodies) and the
	// return value, so allow at least this much per line.
	minLineMax = 4 << 20
	// Raw (non-console) stderr kept for error messages.
	tailMax = 4 << 10
)

var errKilled = errors.New("fnrt: run stopped")

// exec runs one module instance in the given mode ("run" or "inspect") and
// returns the JSON value the runner sent back.
func (e *Engine) exec(parent context.Context, job Job, mode string) (json.RawMessage, int, time.Duration, error) {
	lim := job.Limits.withDefaults()
	input := bytes.TrimSpace(job.Input)
	if len(input) == 0 {
		input = []byte("null")
	} else if !json.Valid(input) {
		return nil, 0, 0, &Error{Code: CodeScript, Message: "input is not valid JSON"}
	}
	jctx := bytes.TrimSpace(job.Context)
	if len(jctx) == 0 {
		jctx = []byte("{}")
	} else if !json.Valid(jctx) {
		return nil, 0, 0, &Error{Code: CodeScript, Message: "context is not valid JSON"}
	}

	tctx, cancel := context.WithTimeout(parent, lim.Timeout)
	defer cancel()
	runCtx, kill := context.WithCancelCause(tctx)
	defer kill(nil)

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stopAfter := context.AfterFunc(runCtx, func() {
		stdinR.CloseWithError(errKilled)
		stdoutR.CloseWithError(errKilled)
	})
	defer stopAfter()

	logs := newLogWriter(job.Log, lim.MaxLogBytes)
	st := &session{
		ctx:      runCtx,
		kill:     kill,
		host:     job.Host,
		maxCalls: lim.MaxHostCalls,
		maxOut:   lim.MaxOutput,
		lineMax:  max(minLineMax, lim.MaxOutput+64<<10),
		logs:     logs,
		replies:  stdinW,
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		st.serve(stdoutR)
	}()

	mem := &memAllocator{limit: uint64(lim.MemoryBytes)}
	fsys := fstest.MapFS{
		"__run.js":     {Data: runnerJS},
		"main.js":      {Data: []byte(job.Code)},
		"input.json":   {Data: input},
		"context.json": {Data: jctx},
	}
	cfg := wazero.NewModuleConfig().
		WithName("").
		WithArgs("qjs", "--std", "-m", "/fn/__run.js", mode).
		WithStdin(stdinR).
		WithStdout(nonEmptyWriter{stdoutW}).
		WithStderr(logs).
		WithFSConfig(wazero.NewFSConfig().WithFSMount(fsys, "/fn")).
		WithSysWalltime().
		WithSysNanotime().
		WithNanosleep(func(ns int64) {
			t := time.NewTimer(time.Duration(ns))
			defer t.Stop()
			select {
			case <-t.C:
			case <-runCtx.Done():
			}
		}).
		WithRandSource(rand.Reader)

	start := time.Now()
	mod, runErr := e.rt.InstantiateModule(experimental.WithMemoryAllocator(runCtx, mem), e.cm, cfg)
	dur := time.Since(start)
	if mod != nil {
		mod.Close(context.Background())
	}
	// The guest is gone: let the reader drain what it wrote, unblock any
	// reply still being written, and wait for the reader to finish.
	stdoutW.Close()
	stdinR.CloseWithError(errKilled)
	<-served
	stdinW.Close()
	logs.flush()

	if st.failure != nil {
		return nil, st.calls, dur, st.failure
	}
	if st.result != nil {
		return st.result, st.calls, dur, nil
	}
	if err := tctx.Err(); err != nil {
		return nil, st.calls, dur, ctxError(err)
	}
	if mem.hit.Load() {
		return nil, st.calls, dur, memoryError()
	}
	if m := st.guestErr; m != nil {
		return nil, st.calls, dur, m.toError()
	}
	tail := strings.TrimSpace(logs.tailString())
	if strings.Contains(strings.ToLower(tail), "out of memory") {
		return nil, st.calls, dur, memoryError()
	}
	msg := "the function stopped without returning (a promise that never settled?)"
	if runErr != nil {
		var exit *sys.ExitError
		if !errors.As(runErr, &exit) {
			msg = runErr.Error()
		} else if exit.ExitCode() != 0 {
			msg = "the function exited with code " + strconv.Itoa(int(exit.ExitCode()))
		}
	}
	if tail != "" {
		msg += ": " + tail
	}
	return nil, st.calls, dur, &Error{Code: CodeScript, Message: msg}
}

func memoryError() *Error {
	return &Error{Code: CodeMemory, Message: "the function ran out of memory"}
}

// guestError is the runner's {t:"error"} message.
type guestError struct {
	Phase   string `json:"phase"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Stack   string `json:"stack"`
}

func (g *guestError) toError() *Error {
	msg := g.Message
	if g.Name != "" && g.Name != "Error" && !strings.HasPrefix(msg, g.Name) {
		msg = g.Name + ": " + msg
	}
	if g.Name == "InternalError" && strings.Contains(g.Message, "out of memory") {
		return &Error{Code: CodeMemory, Message: msg, Stack: g.Stack}
	}
	code := CodeScript
	if g.Phase == "load" {
		code = CodeBadCode
	}
	return &Error{Code: code, Message: msg, Stack: g.Stack}
}

// session is the host side of one run's stdin/stdout protocol. Its fields
// are written only by serve and read after serve returns.
type session struct {
	ctx      context.Context
	kill     context.CancelCauseFunc
	host     HostFunc
	maxCalls int
	maxOut   int
	lineMax  int
	logs     *logWriter
	replies  io.Writer

	calls    int
	result   json.RawMessage
	guestErr *guestError
	failure  *Error
}

func (s *session) fail(err *Error) {
	if s.failure == nil && s.result == nil {
		s.failure = err
	}
	s.kill(errKilled)
}

func (s *session) serve(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br, s.lineMax)
		if err == errLineTooLong {
			s.fail(&Error{Code: CodeOutputTooLarge, Message: "the function sent more than " + strconv.Itoa(s.lineMax) + " bytes in one message"})
			io.Copy(io.Discard, br)
			return
		}
		if len(line) > 0 {
			if line[0] == 1 {
				s.handle(line[1:])
			} else {
				s.logs.emit("log", string(line))
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *session) handle(raw []byte) {
	var head struct {
		T     string          `json:"t"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		s.reply(errorReply(400, "invalid", "malformed request"))
		return
	}
	switch head.T {
	case "return":
		if s.result != nil || s.failure != nil {
			return
		}
		v := head.Value
		if len(v) == 0 {
			v = json.RawMessage("null")
		}
		if len(v) > s.maxOut {
			s.fail(&Error{Code: CodeOutputTooLarge, Message: "the return value is " + strconv.Itoa(len(v)) + " bytes; the limit is " + strconv.Itoa(s.maxOut)})
			return
		}
		s.result = append(json.RawMessage(nil), v...)
	case "error":
		if s.guestErr == nil {
			g := &guestError{}
			json.Unmarshal(raw, g)
			s.guestErr = g
		}
	case "api", "secret", "fetch":
		s.calls++
		if s.calls > s.maxCalls {
			s.fail(&Error{Code: CodeTooManyCalls, Message: "the function made more than " + strconv.Itoa(s.maxCalls) + " host calls"})
			return
		}
		var rep json.RawMessage
		if s.host == nil {
			rep = errorReply(403, "unavailable", "bunker is not available here")
		} else {
			rep = s.host(s.ctx, json.RawMessage(raw))
		}
		s.reply(rep)
	default:
		s.reply(errorReply(400, "invalid", "unknown request type"))
	}
}

func (s *session) reply(rep json.RawMessage) {
	var buf bytes.Buffer
	if len(rep) == 0 || json.Compact(&buf, rep) != nil {
		buf.Reset()
		buf.Write(errorReply(502, "host_error", "the host sent an invalid reply"))
	}
	buf.WriteByte('\n')
	s.replies.Write(buf.Bytes())
}

func errorReply(status int, code, message string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"ok": false, "status": status, "error": map[string]string{"code": code, "message": message}})
	return b
}

var errLineTooLong = errors.New("fnrt: line too long")

// readLine returns the next line without its newline. At EOF it returns the
// trailing partial line along with the error.
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(out)+len(chunk) > limit+1 {
			return nil, errLineTooLong
		}
		if err == bufio.ErrBufferFull {
			out = append(out, chunk...)
			continue
		}
		if out == nil {
			out = chunk
		} else {
			out = append(out, chunk...)
		}
		if err != nil {
			return out, err
		}
		return out[:len(out)-1], nil
	}
}

// memAllocator backs the guest's linear memory and refuses to grow it past
// limit, which QuickJS sees as a failed malloc.
type memAllocator struct {
	limit uint64
	hit   atomic.Bool
}

func (a *memAllocator) Allocate(_, _ uint64) experimental.LinearMemory { return &linearMemory{a: a} }

type linearMemory struct {
	a   *memAllocator
	buf []byte
}

func (m *linearMemory) Reallocate(size uint64) []byte {
	if size > m.a.limit {
		m.a.hit.Store(true)
		return nil
	}
	if size <= uint64(cap(m.buf)) {
		m.buf = m.buf[:size]
		return m.buf
	}
	c := min(max(size, 2*uint64(cap(m.buf))), m.a.limit)
	nb := make([]byte, size, c)
	copy(nb, m.buf)
	m.buf = nb
	return nb
}

func (m *linearMemory) Free() { m.buf = nil }

// logWriter is the guest's stderr. Console lines arrive as
// "\x02<level>\t<JSON string>\n"; anything else is QuickJS itself (uncaught
// errors and the like) and is logged as an error and kept for messages.
type logWriter struct {
	mu        sync.Mutex
	log       func(level, line string)
	left      int
	truncated bool
	partial   []byte
	tail      []byte
}

func newLogWriter(log func(level, line string), maxBytes int) *logWriter {
	return &logWriter{log: log, left: maxBytes}
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.partial = append(w.partial, p...)
			if len(w.partial) > 64<<10 {
				w.lineLocked(w.partial)
				w.partial = w.partial[:0]
			}
			break
		}
		if len(w.partial) > 0 {
			w.partial = append(w.partial, p[:i]...)
			w.lineLocked(w.partial)
			w.partial = w.partial[:0]
		} else {
			w.lineLocked(p[:i])
		}
		p = p[i+1:]
	}
	return n, nil
}

func (w *logWriter) lineLocked(line []byte) {
	if len(line) > 0 && line[0] == 2 {
		level, text, ok := bytes.Cut(line[1:], []byte{'\t'})
		var s string
		if ok && json.Unmarshal(text, &s) == nil {
			w.emitLocked(normLevel(string(level)), s)
			return
		}
		line = line[1:]
	}
	w.tail = append(w.tail, line...)
	w.tail = append(w.tail, '\n')
	if len(w.tail) > tailMax {
		w.tail = append(w.tail[:0], w.tail[len(w.tail)-tailMax:]...)
	}
	w.emitLocked("error", string(line))
}

func normLevel(l string) string {
	switch l {
	case "info", "warn", "error":
		return l
	}
	return "log"
}

func (w *logWriter) emit(level, line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.emitLocked(level, line)
}

func (w *logWriter) emitLocked(level, line string) {
	if w.truncated {
		return
	}
	if len(line) > w.left {
		k := w.left
		for k > 0 && !utf8.RuneStart(line[k]) {
			k--
		}
		line = line[:k]
		w.truncated = true
	}
	w.left -= len(line)
	if w.log != nil && line != "" {
		w.log(level, line)
	}
	if w.left <= 0 && !w.truncated {
		w.truncated = true
	}
	if w.truncated && w.log != nil {
		w.log("warn", "[log truncated: this run reached its log limit]")
	}
}

func (w *logWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 {
		w.lineLocked(w.partial)
		w.partial = nil
	}
}

func (w *logWriter) tailString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.tail)
}

// nonEmptyWriter drops zero-length writes: the guest's libc issues them when
// flushing, and an io.Pipe blocks on them until the reader shows up.
type nonEmptyWriter struct{ w io.Writer }

func (n nonEmptyWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return n.w.Write(p)
}
