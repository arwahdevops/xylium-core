// src/xylium/mode.go
package xylium

import (
	"fmt"  // For formatting panic messages and potentially simple prints.
	"log"  // Standard Go logger for internal framework messages during bootstrap.
	"os"   // For os.Getenv.
	"sync" // For protecting currentGlobalMode.
)

// Constants for Xylium's operating modes.
const (
	DebugMode   string = "debug"
	TestMode    string = "test"
	ReleaseMode string = "release"
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

var (
	// currentGlobalMode holds the active operating mode for Xylium.
	// It defaults to DebugMode initially, but can be overridden by environment or SetMode().
	currentGlobalMode string = DebugMode // Xylium's internal default before any checks.

	// currentGlobalModeLock protects concurrent access to currentGlobalMode and modeSource.
	currentGlobalModeLock sync.RWMutex

	// modeSource tracks how the currentGlobalMode was determined.
	// This is useful for debugging mode precedence.
	modeSource string = "internal_default" // Initial source is the hardcoded default.
)

// init function is called when the xylium package is first imported.
// It performs an initial read of the XYLIUM_MODE environment variable from the system.
// This value can be updated later by updateGlobalModeFromEnvOnRouterInit or SetMode.
func init() {
	// This log prefix helps distinguish Xylium's internal bootstrap messages.
	log.SetPrefix("[XYLIUM-BOOTSTRAP] ") // Set prefix for standard logger used here.

	// This read happens very early, before an application might load a .env file.
	systemEnvMode := os.Getenv(EnvXyliumMode)

	// No lock needed for currentGlobalMode/modeSource here as init() is single-threaded per package.
	if isValidModeInternal(systemEnvMode) {
		currentGlobalMode = systemEnvMode
		modeSource = "env_system_init"
		log.Printf("Mode set to '%s' from system environment variable (%s) at package initialization.", currentGlobalMode, EnvXyliumMode)
	} else {
		// If no valid system env var, it remains the 'internal_default' (DebugMode).
		if systemEnvMode != "" { // If ENV var was set but invalid.
			log.Printf("Warning: Invalid value '%s' for %s in system environment. Using internal default mode '%s'.", systemEnvMode, EnvXyliumMode, currentGlobalMode)
		} else {
			log.Printf("No %s found in system environment at package initialization. Using internal default mode '%s'.", EnvXyliumMode, currentGlobalMode)
		}
	}
	log.SetPrefix("") // Reset prefix for standard logger after Xylium's init.
}

// isValidModeInternal checks if the provided mode string is one of the valid Xylium modes.
// It's an internal helper.
func isValidModeInternal(modeValue string) bool {
	switch modeValue {
	case DebugMode, TestMode, ReleaseMode:
		return true
	default:
		return false
	}
}

// updateGlobalModeFromEnvOnRouterInit is called by router.NewWithConfig().
// It re-reads the XYLIUM_MODE environment variable. This is important because
// an application might have loaded a .env file after Xylium's package init() ran
// but before the router was created. This allows .env values to take effect,
// unless xylium.SetMode() was explicitly called, which has higher precedence.
func updateGlobalModeFromEnvOnRouterInit() {
	currentGlobalModeLock.Lock() // Protect global state.
	defer currentGlobalModeLock.Unlock()

	// Re-read the environment variable. This might now reflect values from a .env file.
	envModeAtRouterCreation := os.Getenv(EnvXyliumMode)

	// Determine the prefix for these specific log messages.
	logPrefix := "[XYLIUM-MODE-UPDATE] "

	// Only proceed if an environment variable is actually set at this stage.
	if envModeAtRouterCreation != "" {
		if isValidModeInternal(envModeAtRouterCreation) {
			// Priority:
			// 1. Explicit xylium.SetMode() ("set_mode_explicit") - Highest.
			// 2. Env var at router init ("env_router_init") - If different from current and not set by SetMode().
			// 3. Env var at package init ("env_system_init").
			// 4. Internal default ("internal_default").

			if modeSource == "set_mode_explicit" {
				// SetMode() was called. It wins. Log if ENV var conflicts.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Warning: Environment variable %s ('%s') differs from explicitly set mode ('%s'). Explicit SetMode() takes precedence.",
						EnvXyliumMode, envModeAtRouterCreation, currentGlobalMode)
				}
				// No change to mode if SetMode was used.
			} else {
				// Mode was not set by SetMode(). ENV var at router init can override previous ENV or default.
				if currentGlobalMode != envModeAtRouterCreation {
					log.Printf(logPrefix+"Xylium mode updated to '%s' from environment variable (%s) at router initialization (was '%s' from '%s').",
						envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource)
					currentGlobalMode = envModeAtRouterCreation
					modeSource = "env_router_init"
				}
				// If currentGlobalMode is already envModeAtRouterCreation (e.g., set by env_system_init and unchanged), no update needed.
			}
		} else { // ENV var is set but invalid.
			log.Printf(logPrefix+"Warning: Invalid value '%s' for %s in environment at router initialization. Current mode '%s' (from '%s') remains unchanged.",
				envModeAtRouterCreation, EnvXyliumMode, currentGlobalMode, modeSource)
		}
	}
	// If envModeAtRouterCreation is empty, do nothing; current mode (from package init or SetMode) is retained.
}

// SetMode explicitly sets Xylium's global operating mode.
// This function has the highest precedence and will override any mode set
// by environment variables or defaults.
// It should typically be called early in the application's main function if needed.
// Panics if an invalid modeValue is provided.
func SetMode(modeValue string) {
	currentGlobalModeLock.Lock() // Protect global state.
	defer currentGlobalModeLock.Unlock()

	if !isValidModeInternal(modeValue) {
		// Use fmt.Sprintf for panic message as log prefix might not be desired here.
		panic(fmt.Sprintf("xylium: invalid mode '%s' provided to SetMode. Use xylium.DebugMode, xylium.TestMode, or xylium.ReleaseMode.", modeValue))
	}

	logPrefix := "[XYLIUM-SET-MODE] "
	// Update only if the mode is changing or if the source was different.
	if currentGlobalMode != modeValue || modeSource != "set_mode_explicit" {
		log.Printf(logPrefix+"Xylium global operating mode explicitly set to '%s' (was '%s' from '%s'). This overrides environment variables.",
			modeValue, currentGlobalMode, modeSource)
		currentGlobalMode = modeValue
		modeSource = "set_mode_explicit" // Mark that mode was set explicitly.
	} else {
		log.Printf(logPrefix+"Xylium global operating mode already '%s' (explicitly set). Call to SetMode with the same value ignored.", modeValue)
	}
}

// Mode returns the current global operating mode of Xylium.
// Applications and Xylium components (like the Router) use this function
// to get the effective operating mode.
func Mode() string {
	currentGlobalModeLock.RLock() // Read lock for accessing global state.
	defer currentGlobalModeLock.RUnlock()
	// Logging Mode() calls can be very noisy, so it's generally avoided
	// unless specific debug flags are enabled for Xylium itself.
	return currentGlobalMode
}
