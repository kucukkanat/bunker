// Package fnrt runs server functions: bundled ES modules executed in a fresh
// QuickJS (quickjs-ng, compiled to WASI) instance inside wazero for every call.
//
// The engine knows nothing about Bunker. The guest talks to the embedding
// application only through Job.Host (one JSON message per call) and reports
// logs through Job.Log. Every run gets its own linear memory, capped by
// Limits.MemoryBytes, and is killed when Limits.Timeout passes.
package fnrt

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed qjs-wasi.wasm
var qjsWasm []byte

//go:embed runner.js
var runnerJS []byte

// Error codes returned in Error.Code.
const (
	CodeBusy           = "busy"
	CodeTimeout        = "timeout"
	CodeMemory         = "memory"
	CodeScript         = "script"
	CodeOutputTooLarge = "output_too_large"
	CodeTooManyCalls   = "too_many_calls"
	CodeBadCode        = "bad_code"
)

// Default limits, used for any zero field of Limits.
const (
	DefaultMemoryBytes  = 64 << 20
	DefaultTimeout      = 30 * time.Second
	DefaultMaxOutput    = 1 << 20
	DefaultMaxLogBytes  = 64 << 10
	DefaultMaxHostCalls = 1000

	inspectTimeout = time.Second
	busyWait       = 100 * time.Millisecond
	minMemoryBytes = 8 << 20
)

// Config configures an Engine.
type Config struct {
	// MaxConcurrent caps simultaneous runs (Run and Inspect).
	// Zero means max(4, 2×NumCPU).
	MaxConcurrent int
	// CacheDir, when set, keeps wazero's compiled code on disk so a
	// restart skips the ~300 ms compile.
	CacheDir string
}

// Limits bounds a single run. Zero fields take the Default* values.
type Limits struct {
	MemoryBytes  int64         // total guest linear memory
	Timeout      time.Duration // wall clock for the whole run
	MaxOutput    int           // bytes of the JSON return value
	MaxLogBytes  int           // bytes of console output passed to Log
	MaxHostCalls int           // api/secret/fetch requests
}

// HostFunc answers one guest request. msg is the request object exactly as
// the guest sent it ({t:"api"|"secret"|"fetch", ...}); the reply must be
// {ok:true, status, data} or {ok:false, status, error:{code, message}}.
type HostFunc func(ctx context.Context, msg json.RawMessage) json.RawMessage

// Job is one function call.
type Job struct {
	Code    string          // bundled ES module with a default export
	Input   json.RawMessage // first argument of the default export (null when empty)
	Context json.RawMessage // second argument, ctx (an empty object when empty)
	Limits  Limits
	Host    HostFunc                 // nil refuses every request
	Log     func(level, line string) // nil drops logs; level is log|info|warn|error
}

// Result is a successful run.
type Result struct {
	Output    json.RawMessage
	Duration  time.Duration
	HostCalls int
}

// Error is every failure Run and Inspect report.
type Error struct {
	Code    string
	Message string
	Stack   string
	cause   error
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "fnrt: " + e.Code
	}
	return "fnrt: " + e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause (for example context.Canceled).
func (e *Error) Unwrap() error { return e.cause }

// Engine runs jobs. It is safe for concurrent use.
type Engine struct {
	cfg Config
	sem chan struct{}

	once    sync.Once
	rt      wazero.Runtime
	cache   wazero.CompilationCache
	cm      wazero.CompiledModule
	initErr error

	mu     sync.Mutex
	closed bool
}

// New returns an engine. The QuickJS module is compiled on first use.
func New(cfg Config) *Engine {
	n := cfg.MaxConcurrent
	if n <= 0 {
		n = max(4, 2*runtime.NumCPU())
	}
	return &Engine{cfg: cfg, sem: make(chan struct{}, n)}
}

// Close releases the compiled module and runtime. Runs in progress are killed.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.once.Do(func() { e.initErr = errors.New("fnrt: engine closed") })
	var err error
	if e.rt != nil {
		err = e.rt.Close(ctx)
	}
	if e.cache != nil {
		if cerr := e.cache.Close(ctx); err == nil {
			err = cerr
		}
	}
	return err
}

func (e *Engine) init() error {
	e.once.Do(func() {
		ctx := context.Background()
		rc := wazero.NewRuntimeConfigCompiler().WithCloseOnContextDone(true).WithDebugInfoEnabled(false)
		if e.cfg.CacheDir != "" {
			c, err := wazero.NewCompilationCacheWithDir(e.cfg.CacheDir)
			if err != nil {
				e.initErr = fmt.Errorf("fnrt: compilation cache: %w", err)
				return
			}
			e.cache = c
			rc = rc.WithCompilationCache(c)
		}
		rt := wazero.NewRuntimeWithConfig(ctx, rc)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
			rt.Close(ctx)
			e.initErr = fmt.Errorf("fnrt: wasi: %w", err)
			return
		}
		cm, err := rt.CompileModule(ctx, qjsWasm)
		if err != nil {
			rt.Close(ctx)
			e.initErr = fmt.Errorf("fnrt: compile: %w", err)
			return
		}
		e.rt, e.cm = rt, cm
	})
	if e.initErr != nil {
		return e.initErr
	}
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return errors.New("fnrt: engine closed")
	}
	return nil
}

// Warm compiles the QuickJS module now instead of on the first run.
func (e *Engine) Warm() error { return e.init() }

func (e *Engine) acquire(ctx context.Context) error {
	select {
	case e.sem <- struct{}{}:
		return nil
	default:
	}
	t := time.NewTimer(busyWait)
	defer t.Stop()
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-t.C:
		return &Error{Code: CodeBusy, Message: "too many functions are running; try again shortly"}
	case <-ctx.Done():
		return ctxError(ctx.Err())
	}
}

func (e *Engine) release() { <-e.sem }

// Run executes job.Code's default export with job.Input and job.Context and
// returns its JSON-encoded result. Failures are *Error.
func (e *Engine) Run(ctx context.Context, job Job) (*Result, error) {
	if err := e.init(); err != nil {
		return nil, err
	}
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()
	out, calls, dur, err := e.exec(ctx, job, "run")
	if err != nil {
		return nil, err
	}
	return &Result{Output: out, Duration: dur, HostCalls: calls}, nil
}

// Inspect evaluates the module (1 s, no host access) and returns its
// `config` export as JSON ("null" when there is none). A module without a
// default export function is a bad_code error.
func (e *Engine) Inspect(ctx context.Context, code string) (json.RawMessage, error) {
	if err := e.init(); err != nil {
		return nil, err
	}
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.release()
	job := Job{Code: code, Limits: Limits{Timeout: inspectTimeout, MaxHostCalls: 100}}
	out, _, _, err := e.exec(ctx, job, "inspect")
	if err != nil {
		if fe, ok := err.(*Error); ok && fe.Code == CodeScript {
			fe.Code = CodeBadCode
		}
		return nil, err
	}
	var r struct {
		Config     json.RawMessage `json:"config"`
		HasDefault bool            `json:"hasDefault"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, &Error{Code: CodeBadCode, Message: "could not read the module's config"}
	}
	if !r.HasDefault {
		return nil, &Error{Code: CodeBadCode, Message: "the module has no default export function"}
	}
	if len(r.Config) == 0 {
		r.Config = json.RawMessage("null")
	}
	return r.Config, nil
}

func ctxError(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Message: "the function ran out of time", cause: err}
	}
	return &Error{Code: CodeTimeout, Message: "the run was cancelled", cause: err}
}

func (l Limits) withDefaults() Limits {
	if l.MemoryBytes <= 0 {
		l.MemoryBytes = DefaultMemoryBytes
	}
	if l.MemoryBytes < minMemoryBytes {
		l.MemoryBytes = minMemoryBytes
	}
	if l.Timeout <= 0 {
		l.Timeout = DefaultTimeout
	}
	if l.MaxOutput <= 0 {
		l.MaxOutput = DefaultMaxOutput
	}
	if l.MaxLogBytes <= 0 {
		l.MaxLogBytes = DefaultMaxLogBytes
	}
	if l.MaxHostCalls <= 0 {
		l.MaxHostCalls = DefaultMaxHostCalls
	}
	return l
}
