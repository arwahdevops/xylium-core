package xylium

import (
	"fmt"  // For formatting panic messages and potentially simple prints.
	"log"  // Standard Go logger for internal framework messages during bootstrap.
	"os"   // For os.Getenv.
	"sync" // For protecting currentGlobalMode.
)

// Constants for Xylium's operating modes. These strings define the valid
// modes that the framework can operate in, affecting behavior such as
// logging verbosity (e.g., DefaultLogger configuration) and error message details.
const (
	DebugMode   string = "debug"   // DebugMode enables verbose logging, detailed error messages, and other developer aids. Default mode.
	TestMode    string = "test"    // TestMode is typically used during automated testing, often with debug-like verbosity but potentially without certain developer aids like colored logs.
	ReleaseMode string = "release" // ReleaseMode is optimized for production, with less verbose logging and generic error messages to clients.
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's
// operating mode. If this variable is set with a valid mode string, Xylium will
// attempt to use its value to configure the global operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

var (
	// currentGlobalMode holds the active operating mode for the Xylium framework.
	// It defaults to DebugMode and can be overridden by environment variables or
	// an explicit call to SetMode(). It's read by xylium.Mode().
	currentGlobalMode string = DebugMode // Internal default before any checks.

	// currentGlobalModeLock protects concurrent access to currentGlobalMode and modeSource.
	// This ensures that mode changes and reads are thread-safe, crucial if SetMode
	// could theoretically be called from different goroutines (though typically called once at startup).
	currentGlobalModeLock sync.RWMutex

	// modeSource tracks how currentGlobalMode was determined.
	// Values: "internal_default", "env_system_init", "env_router_init", "set_mode_explicit".
	// This is primarily for debugging and understanding mode precedence.
	modeSource string = "internal_default"
)

// init function is called when the xylium package is first imported (once per application).
// It performs an initial read of the XYLIUM_MODE environment variable from the system environment.
// This allows setting the mode globally very early in the application lifecycle,
// even before the main router is initialized or .env files are loaded.
// The standard `log` package is used for bootstrap messages here as Xylium's
// own logger might not be fully configured yet.
func init() {
	// Using a distinct prefix for Xylium's internal bootstrap messages
	// helps differentiate them from application logs or other library logs.
	log.SetPrefix("[XYLIUM-BOOTSTRAP] ")
	defer log.SetPrefix("") // Reset prefix for standard logger after init.

	// Read the system environment variable at package load time.
	systemEnvMode := os.Getenv(EnvXyliumMode)

	// No lock needed for currentGlobalMode/modeSource here as init() is guaranteed
	// to be single-threaded per package by Go's runtime.
	if isValidModeInternal(systemEnvMode) {
		currentGlobalMode = systemEnvMode
		modeSource = "env_system_init" // Mode set from system environment variable during package init.
		log.Printf("Mode set to '%s' from system environment variable (%s) at package initialization.", currentGlobalMode, EnvXyliumMode)
	} else {
		// If no valid system env var, it remains the 'internal_default' (DebugMode).
		if systemEnvMode != "" { // If ENV var was set but contained an invalid mode string.
			log.Printf("Warning: Invalid value '%s' for %s in system environment. Using internal default mode '%s'. Valid modes: %s, %s, %s.",
				systemEnvMode, EnvXyliumMode, currentGlobalMode, DebugMode, TestMode, ReleaseMode)
		} else {
			// No XYLIUM_MODE environment variable found at this early stage.
			log.Printf("No %s found in system environment at package initialization. Using internal default mode '%s'.", EnvXyliumMode, currentGlobalMode)
		}
	}
}

// isValidModeInternal checks if the provided mode string is one of the valid Xylium modes.
// This is an internal helper to centralize mode validation.
func isValidModeInternal(modeValue string) bool {
	switch modeValue {
	case DebugMode, TestMode, ReleaseMode:
		return true
	default:
		return false
	}
}

// updateGlobalModeFromEnvOnRouterInit is called internally by router.NewWithConfig().
// Its purpose is to re-read the XYLIUM_MODE environment variable at the point
// when the Xylium router is being initialized. This is crucial because an application
// might load environment variables from a .env file (e.g., using a library like godotenv)
// *after* Xylium's package `init()` function has already run, but *before* the
// `xylium.New()` or `xylium.NewWithConfig()` call. This function allows such .env
// values to take effect for determining the mode, respecting the overall precedence.
//
// Mode Precedence Order (Highest to Lowest):
// 1. Explicit call to `xylium.SetMode()` (`modeSource`: "set_mode_explicit").
// 2. Environment variable `XYLIUM_MODE` read at router initialization (this function) (`modeSource`: "env_router_init").
// 3. Environment variable `XYLIUM_MODE` read at package initialization (`init()` func) (`modeSource`: "env_system_init").
// 4. Internal default framework mode (`modeSource`: "internal_default", which is DebugMode).
func updateGlobalModeFromEnvOnRouterInit() {
	currentGlobalModeLock.Lock() // Ensure exclusive access to global mode state.
	defer currentGlobalModeLock.Unlock()

	// Re-read the environment variable. This might now reflect values from a .env file.
	envModeAtRouterCreation := os.Getenv(EnvXyliumMode)
	logPrefix := "[XYLIUM-MODE-UPDATE] " // Prefix for logging mode update attempts.

	// Only proceed if an environment variable is actually set at this stage.
	if envModeAtRouterCreation != "" {
		if isValidModeInternal(envModeAtRouterCreation) {
			// If SetMode() was called previously, it takes the highest precedence and cannot be overridden by ENV var here.
			if modeSource == "set_mode_explicit" {
				// Log a warning if the environment variable conflicts with the explicitly set mode.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Warning: Environment variable %s ('%s') differs from explicitly set mode ('%s'). Explicit SetMode() call takes precedence.",
						EnvXyliumMode, envModeAtRouterCreation, currentGlobalMode)
				}
				// currentGlobalMode remains unchanged as SetMode() has higher priority.
			} else {
				// Mode was not set by SetMode(). The ENV var read at router initialization
				// can override a previous ENV setting (from package init) or the internal default.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Xylium mode updated to '%s' from environment variable (%s) at router initialization (was '%s' from '%s').",
						envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource)
					currentGlobalMode = envModeAtRouterCreation
					modeSource = "env_router_init" // Mark that ENV var at router init set the mode.
				} else if modeSource == "env_system_init" && currentGlobalMode == envModeAtRouterCreation {
					// If system env already set it and .env didn't change it, just update source for clarity.
					modeSource = "env_router_init"
					// No log message needed if value is same, only source tracking changes.
				}
				// If currentGlobalMode is already envModeAtRouterCreation and source was "internal_default",
				// the log message above handles it.
			}
		} else { // ENV var is set but contains an invalid mode string.
			log.Printf(logPrefix+"Warning: Invalid value '%s' for %s in environment at router initialization. Current mode '%s' (from '%s') remains unchanged. Valid modes: %s, %s, %s.",
				envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource, DebugMode, TestMode, ReleaseMode)
		}
	}
	// If envModeAtRouterCreation is empty, do nothing; the current mode (from package init,
	// internal default, or an explicit SetMode call) is retained.
}

// SetMode explicitly sets Xylium's global operating mode.
// This function has the highest precedence and will override any mode set
// by environment variables or the internal framework default.
// It should typically be called early in the application's `main()` function,
// ideally before `xylium.New()` or `xylium.NewWithConfig()`, if programmatic
// control over the mode is desired.
// Panics if an invalid `modeValue` (not DebugMode, TestMode, or ReleaseMode) is provided.
func SetMode(modeValue string) {
	currentGlobalModeLock.Lock() // Ensure exclusive access to global mode state.
	defer currentGlobalModeLock.Unlock()

	if !isValidModeInternal(modeValue) {
		// Use fmt.Sprintf for panic message as log prefix might not be standard here.
		panic(fmt.Sprintf("xylium: invalid mode '%s' provided to SetMode. Use xylium.DebugMode (%q), xylium.TestMode (%q), or xylium.ReleaseMode (%q).",
			modeValue, DebugMode, TestMode, ReleaseMode))
	}

	logPrefix := "[XYLIUM-SET-MODE] " // Prefix for logging explicit mode setting.

	// Update only if the mode is changing or if the source was different (e.g., from ENV to explicit).
	if currentGlobalMode != modeValue || modeSource != "set_mode_explicit" {
		log.Printf(logPrefix+"Xylium global operating mode explicitly set to '%s' (was '%s' from '%s'). This overrides any environment variable settings.",
			modeValue, currentGlobalMode, modeSource)
		currentGlobalMode = modeValue
		modeSource = "set_mode_explicit" // Mark that mode was set explicitly by this function.
	} else {
		// If SetMode is called multiple times with the same value that was already explicitly set.
		log.Printf(logPrefix+"Xylium global operating mode is already '%s' (explicitly set). Call to SetMode with the same value had no effect.", modeValue)
	}
}

// Mode returns the current global operating mode of Xylium (e.g., "debug", "test", "release").
// Applications and Xylium components (like the Router and DefaultLogger) use this
// function to get the effective operating mode, which determines various behaviors.
// This function is thread-safe for reading the mode.
func Mode() string {
	currentGlobalModeLock.RLock() // Read lock for accessing global state.
	defer currentGlobalModeLock.RUnlock()
	// Logging Mode() calls directly can be very noisy and is generally avoided here
	// unless specific debug flags are enabled for Xylium's internal operations.
	return currentGlobalMode
}
