package xylium

// This file contains helper functions intended for use ONLY within the internal
// unit tests of the xylium package. These functions may access unexported fields
// or methods of xylium structs to facilitate testing.
// DO NOT USE these helpers outside the context of xylium package's own tests.

import (
	"io"  // For io.Discard
	"log" // Standard Go logger for silencing during test setup
	"sync"

	"github.com/valyala/fasthttp" // For fasthttp.RequestCtx
)

// NewContextForTest creates a new, minimal Context instance suitable for testing purposes.
// It allows direct initialization of fields like `Params` and `fasthttp.RequestCtx`
// that are normally managed by the router or context pool.
//
// Parameters:
//   - params: A map of route parameters to initialize the context with. Can be nil.
//   - fasthttpCtx: An optional `*fasthttp.RequestCtx`. If nil, a new minimal one is created.
//
// WARNING: This function is intended for internal testing of the xylium package only.
// Its signature or behavior might change without notice.
func NewContextForTest(params map[string]string, fasthttpCtx *fasthttp.RequestCtx) *Context {
	if fasthttpCtx == nil {
		fasthttpCtx = &fasthttp.RequestCtx{} // Provide a minimal fasthttp.RequestCtx if nil.
	}
	if params == nil {
		params = make(map[string]string) // Initialize if nil.
	}

	// Initialize context similar to pool.go's New(), but with potentially pre-filled params.
	// mu and store are initialized as they would be by the pool.
	return &Context{
		Ctx:    fasthttpCtx,
		Params: params,
		store:  make(map[string]interface{}),
		mu:     new(sync.RWMutex),
		index:  -1, // Default for a new context before handler execution.
		// router, handlers, queryArgs, formArgs, goCtx will default to nil/zero.
		// These can be set specifically by tests using other helpers if needed.
	}
}

// SetRouterForTesting allows setting the unexported `router` field of a Context
// for testing purposes. This is useful for testing Context methods that depend
// on the router, such as `c.Logger()` or `c.AppGet()`.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) SetRouterForTesting(r *Router) {
	c.router = r
}

// SetHandlersForTesting allows setting the unexported `handlers` field and resetting
// the `index` for testing middleware execution logic via `c.Next()`.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) SetHandlersForTesting(handlers []HandlerFunc) {
	c.handlers = handlers
	c.index = -1 // Reset index for a new handler chain.
}

// GetContextStoreForTesting provides direct, unlocked access to the context's
// internal store map. This bypasses the context's mutex.
//
// WARNING: This function is intended for internal testing of the xylium package only.
// Use with extreme caution, primarily for inspection or setup in tests where
// concurrent access is not a concern. Prefer using `c.Set()` and `c.Get()` for
// normal store interactions in tests.
func (c *Context) GetContextStoreForTesting() map[string]interface{} {
	return c.store
}

// RouterTestOptions holds options for creating a router for testing.
type RouterTestOptions struct {
	Mode        string       // Desired Xylium operating mode (e.g., xylium.DebugMode, xylium.TestMode). If empty, current global mode is used.
	SilenceLogs bool         // If true, silences standard Go logs and Xylium bootstrap logs during router creation.
	Config      ServerConfig // Custom ServerConfig to use. If zero, DefaultServerConfig is used.
}

// NewRouterForTesting creates a Router instance suitable for testing.
// It allows specifying the operating mode and can silence bootstrap logs for cleaner test output.
//
// Parameters:
//   - opts: RouterTestOptions to customize router creation.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func NewRouterForTesting(opts ...RouterTestOptions) *Router {
	opt := RouterTestOptions{
		SilenceLogs: true, // Default to silencing logs for cleaner test output.
	}
	if len(opts) > 0 {
		opt = opts[0]
	}

	var originalStdLogOutput io.Writer
	if opt.SilenceLogs {
		// Save the original standard Go logger's output.
		originalStdLogOutput = log.Writer()
		// Silence the standard Go logger (used by Xylium's bootstrap messages).
		log.SetOutput(io.Discard)
	}

	originalGlobalMode := ""
	if opt.Mode != "" {
		// Save the current global Xylium mode if we are going to change it.
		originalGlobalMode = Mode()
		// Set the desired mode for this test router.
		// This will print a bootstrap log if SilenceLogs is false or if mode changes.
		SetMode(opt.Mode)
	}

	// Determine the ServerConfig to use.
	var cfg ServerConfig
	// Check if a non-zero ServerConfig was provided in options.
	// A simple check is if Name is set, as DefaultServerConfig sets it.
	// More robust: check if opts[0].Config is not its zero value, if ServerConfig had a clear zero state.
	// For now, this check should be okay if opts[0].Config is explicitly provided.
	if len(opts) > 0 && opts[0].Config.Name != "" { // Basic check if a config was passed
		cfg = opts[0].Config
	} else {
		cfg = DefaultServerConfig()
	}

	// NewWithConfig will:
	// 1. Call updateGlobalModeFromEnvOnRouterInit() (which might log if SilenceLogs is false).
	// 2. Determine effectiveMode based on current global mode.
	// 3. Initialize its own logger based on effectiveMode and cfg.LoggerConfig.
	//    If SilenceLogs is true, its bootstrap "Xylium Router initialized..." log will go to io.Discard
	//    if its own logger ends up being the standard logger. However, Xylium's DefaultLogger
	//    uses its own output writer (default os.Stdout), so this bootstrap log from Xylium's
	//    own logger might still appear unless cfg.LoggerConfig.Output is also set to io.Discard.
	//    To fully silence Xylium's logger, we'd need to pass a silenced logger in cfg.
	if opt.SilenceLogs {
		// If silencing is requested, ensure the router we create also uses a silent logger
		// for its own initialization messages, if it's creating a DefaultLogger.
		if cfg.Logger == nil { // Only if Xylium is going to create a DefaultLogger
			if cfg.LoggerConfig == nil {
				defaultCfg := DefaultLoggerConfig()
				cfg.LoggerConfig = &defaultCfg
			}
			cfg.LoggerConfig.Output = io.Discard
			// Ensure color is also off to prevent escape codes in discarded output.
			cfg.LoggerConfig.UseColor = false
		}
	}

	router := NewWithConfig(cfg)

	// Restore original global Xylium mode if it was changed.
	if originalGlobalMode != "" && originalGlobalMode != Mode() {
		SetMode(originalGlobalMode) // This might also log if SilenceLogs is false.
	}

	// Restore the original standard Go logger's output if it was silenced.
	if opt.SilenceLogs && originalStdLogOutput != nil {
		log.SetOutput(originalStdLogOutput)
	}

	return router
}

// FasthttpCtxGetQueryArgsForTesting directly accesses and returns the QueryArgs from `fasthttp.RequestCtx`.
// This can be useful if you need to manipulate or inspect `fasthttp.Args` directly in tests,
// though generally `c.QueryParam()` or `c.Bind()` are preferred.
// Returns nil if `c.Ctx` is nil.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) FasthttpCtxGetQueryArgsForTesting() *fasthttp.Args {
	if c.Ctx == nil {
		return nil
	}
	return c.Ctx.QueryArgs() // This ensures QueryArgs are parsed if not already.
}

// FasthttpCtxGetPostArgsForTesting directly accesses and returns the PostArgs from `fasthttp.RequestCtx`.
// This ensures PostArgs are parsed if not already. Useful for direct manipulation or inspection in tests.
// Returns nil if `c.Ctx` is nil.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) FasthttpCtxGetPostArgsForTesting() *fasthttp.Args {
	if c.Ctx == nil {
		return nil
	}
	_ = c.Ctx.PostArgs() // Ensures PostArgs are parsed and available.
	return c.Ctx.PostArgs()
}

// GetParamsForTesting returns a copy of the internal `Params` map (route parameters).
// Useful for inspecting path parameters set on the context during tests.
// Returns nil if `c.Params` is nil.
//
// WARNING: This function is intended for internal testing of the xylium package only.
func (c *Context) GetParamsForTesting() map[string]string {
	if c.Params == nil {
		return nil
	}
	// Return a copy to prevent external modification of the internal map.
	paramsCopy := make(map[string]string, len(c.Params))
	for k, v := range c.Params {
		paramsCopy[k] = v
	}
	return paramsCopy
}

// GetContextResponseOnceForTesting exposes the `responseOnce` field for advanced testing scenarios.
// This might be used to check if it has been triggered or to manually trigger it
// in a controlled test environment.
//
// WARNING: This function is intended for internal testing of the xylium package only.
// Manipulating `sync.Once` directly is generally not recommended outside of careful testing.
func (c *Context) GetContextResponseOnceForTesting() *sync.Once {
	return &c.responseOnce
}
