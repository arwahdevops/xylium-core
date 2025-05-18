package xylium

import (
	"fmt"  // For formatting panic messages and potentially simple prints.
	"log"  // Standard Go logger for internal framework messages during bootstrap.
	"os"   // For os.Getenv.
	"sync" // For protecting currentGlobalMode.
)

// Constants for Xylium's operating modes. These strings define the valid
// modes that the framework can operate in, affecting behavior such as
// logging verbosity and error message details.
const (
	DebugMode   string = "debug"   // DebugMode enables verbose logging, detailed error messages, and other developer aids.
	TestMode    string = "test"    // TestMode is typically used during automated testing, often with debug-like verbosity but without certain developer aids like colored logs.
	ReleaseMode string = "release" // ReleaseMode is optimized for production, with less verbose logging and generic error messages.
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's
// operating mode. If this variable is set, Xylium will attempt to use its
// value to configure the operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

var (
	// currentGlobalMode holds the active operating mode for the Xylium framework.
	// It defaults to DebugMode and can be overridden by environment variables or
	// an explicit call to SetMode().
	currentGlobalMode string = DebugMode // Internal default before any checks.

	// currentGlobalModeLock protects concurrent access to currentGlobalMode and modeSource.
	// This ensures that mode changes and reads are thread-safe.
	currentGlobalModeLock sync.RWMutex

	// modeSource tracks how currentGlobalMode was determined (e.g., "internal_default",
	// "env_system_init", "env_router_init", "set_mode_explicit").
	// This is primarily for debugging and understanding mode precedence.
	modeSource string = "internal_default"
)

// init function is called when the xylium package is first imported.
// It performs an initial read of the XYLIUM_MODE environment variable.
// This allows setting the mode globally very early in the application lifecycle,
// even before the main router is initialized. The standard `log` package is used
// for bootstrap messages here as Xylium's logger might not be fully configured yet.
func init() {
	log.SetPrefix("[XYLIUM-BOOTSTRAP] ") // Distinguish Xylium's internal bootstrap messages.

	// Read the system environment variable at package load time.
	// This value can be updated later by updateGlobalModeFromEnvOnRouterInit (if an app uses .env files)
	// or overridden by an explicit SetMode() call.
	systemEnvMode := os.Getenv(EnvXyliumMode)

	// No lock needed for currentGlobalMode/modeSource here as init() is single-threaded per package.
	if isValidModeInternal(systemEnvMode) {
		currentGlobalMode = systemEnvMode
		modeSource = "env_system_init" // Mode set from environment variable during package init.
		log.Printf("Mode set to '%s' from system environment variable (%s) at package initialization.", currentGlobalMode, EnvXyliumMode)
	} else {
		// If no valid system env var, it remains the 'internal_default' (DebugMode).
		if systemEnvMode != "" { // If ENV var was set but invalid.
			log.Printf("Warning: Invalid value '%s' for %s in system environment. Using internal default mode '%s'.", systemEnvMode, EnvXyliumMode, currentGlobalMode)
		} else {
			// No ENV var found at this early stage.
			log.Printf("No %s found in system environment at package initialization. Using internal default mode '%s'.", EnvXyliumMode, currentGlobalMode)
		}
	}
	log.SetPrefix("") // Reset prefix for standard logger.
}

// isValidModeInternal checks if the provided mode string is one of the valid Xylium modes.
// This is an internal helper to avoid string comparisons in multiple places.
func isValidModeInternal(modeValue string) bool {
	switch modeValue {
	case DebugMode, TestMode, ReleaseMode:
		return true
	default:
		return false
	}
}

// updateGlobalModeFromEnvOnRouterInit is called internally by router.NewWithConfig().
// It re-reads the XYLIUM_MODE environment variable. This is important because
// an application might have loaded a .env file (e.g., using a library like godotenv)
// *after* Xylium's package `init()` ran but *before* the Xylium router was created.
// This function allows such .env values to take effect for the mode, unless
// `xylium.SetMode()` was explicitly called, as `SetMode()` has the highest precedence.
//
// Mode Precedence Order (Highest to Lowest):
// 1. Explicit call to `xylium.SetMode()` (`modeSource`: "set_mode_explicit").
// 2. Environment variable `XYLIUM_MODE` read at router initialization (`modeSource`: "env_router_init").
// 3. Environment variable `XYLIUM_MODE` read at package initialization (`modeSource`: "env_system_init").
// 4. Internal default framework mode (`modeSource`: "internal_default", defaults to DebugMode).
func updateGlobalModeFromEnvOnRouterInit() {
	currentGlobalModeLock.Lock() // Ensure exclusive access to global mode state.
	defer currentGlobalModeLock.Unlock()

	// Re-read the environment variable. This might now reflect values from a .env file.
	envModeAtRouterCreation := os.Getenv(EnvXyliumMode)
	logPrefix := "[XYLIUM-MODE-UPDATE] " // Prefix for logging mode update attempts.

	// Only proceed if an environment variable is actually set at this stage.
	if envModeAtRouterCreation != "" {
		if isValidModeInternal(envModeAtRouterCreation) {
			// If SetMode() was called, it takes highest precedence.
			if modeSource == "set_mode_explicit" {
				// Log a warning if the environment variable conflicts with the explicitly set mode.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Warning: Environment variable %s ('%s') differs from explicitly set mode ('%s'). Explicit SetMode() takes precedence.",
						EnvXyliumMode, envModeAtRouterCreation, currentGlobalMode)
				}
				// currentGlobalMode remains unchanged as SetMode() has higher priority.
			} else {
				// Mode was not set by SetMode(). ENV var at router init can override
				// previous ENV setting from package init or the internal default.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Xylium mode updated to '%s' from environment variable (%s) at router initialization (was '%s' from '%s').",
						envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource)
					currentGlobalMode = envModeAtRouterCreation
					modeSource = "env_router_init" // Mark that ENV var at router init set the mode.
				}
				// If currentGlobalMode is already envModeAtRouterCreation (e.g., set by env_system_init
				// and no .env file changed it), no update or log message is needed here.
			}
		} else { // ENV var is set but contains an invalid mode string.
			log.Printf(logPrefix+"Warning: Invalid value '%s' for %s in environment at router initialization. Current mode '%s' (from '%s') remains unchanged.",
				envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource)
		}
	}
	// If envModeAtRouterCreation is empty, do nothing; the current mode (from package init,
	// internal default, or SetMode) is retained.
}

// SetMode explicitly sets Xylium's global operating mode.
// This function has the highest precedence and will override any mode set
// by environment variables or the internal framework default.
// It should typically be called early in the application's `main()` function,
// before router initialization, if programmatic control over the mode is desired.
// Panics if an invalid `modeValue` is provided.
func SetMode(modeValue string) {
	currentGlobalModeLock.Lock() // Ensure exclusive access to global mode state.
	defer currentGlobalModeLock.Unlock()

	if !isValidModeInternal(modeValue) {
		// Use fmt.Sprintf for panic message as log prefix might not be standard here.
		panic(fmt.Sprintf("xylium: invalid mode '%s' provided to SetMode. Use xylium.DebugMode, xylium.TestMode, or xylium.ReleaseMode.", modeValue))
	}

	logPrefix := "[XYLIUM-SET-MODE] " // Prefix for logging explicit mode setting.

	// Update only if the mode is changing or if the source was different (e.g., from ENV to explicit).
	if currentGlobalMode != modeValue || modeSource != "set_mode_explicit" {
		log.Printf(logPrefix+"Xylium global operating mode explicitly set to '%s' (was '%s' from '%s'). This overrides environment variables.",
			modeValue, currentGlobalMode, modeSource)
		currentGlobalMode = modeValue
		modeSource = "set_mode_explicit" // Mark that mode was set explicitly by this function.
	} else {
		// If SetMode is called multiple times with the same value that was already explicitly set.
		log.Printf(logPrefix+"Xylium global operating mode already '%s' (explicitly set). Call to SetMode with the same value ignored.", modeValue)
	}
}

// Mode returns the current global operating mode of Xylium (e.g., "debug", "test", "release").
// Applications and Xylium components (like the Router and DefaultLogger) use this
// function to get the effective operating mode, which determines various behaviors.
// This function is thread-safe for reading the mode.
func Mode() string {
	currentGlobalModeLock.RLock() // Read lock for accessing global state.
	defer currentGlobalModeLock.RUnlock()
	// Logging Mode() calls directly can be very noisy, so it's generally avoided
	// unless specific debug flags are enabled for Xylium's internal operations.
	return currentGlobalMode
}
