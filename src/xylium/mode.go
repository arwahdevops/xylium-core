// src/xylium/mode.go
package xylium

import (
	"fmt"
	"log" // Standard log for internal framework messages.
	"os"
	"sync" // For protecting currentGlobalMode.
)

// Constants for Xylium's operating modes.
const (
	// DebugMode enables verbose logging, detailed error messages, and other debugging aids.
	DebugMode string = "debug"
	// TestMode is typically used for running automated tests, might alter behavior like logging.
	TestMode string = "test"
	// ReleaseMode is optimized for production, with less verbose logging and potentially performance gains.
	ReleaseMode string = "release"
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

var (
	// currentGlobalMode holds the active operating mode for Xylium.
	// It defaults to DebugMode if not overridden by environment or SetMode().
	currentGlobalMode string = DebugMode // Default to DebugMode

	// currentGlobalModeLock protects concurrent access to currentGlobalMode and modeSource.
	currentGlobalModeLock sync.RWMutex

	// modeSource tracks how the currentGlobalMode was determined.
	// Possible values:
	//   - "internal_default": Xylium's hardcoded default (DebugMode).
	//   - "env_system_init": Set by an environment variable present when Xylium package was initialized.
	//   - "env_router_init": Set by an environment variable read when a Xylium router instance was created.
	//                        This allows .env files loaded by the application to take effect.
	//   - "set_mode_explicit": Set by an explicit call to xylium.SetMode().
	modeSource string = "internal_default"
)

// init function is called when the xylium package is first imported.
// It attempts an initial read of the XYLIUM_MODE environment variable from the system environment.
// This value can be later updated or overridden.
func init() {
	// This read happens before an application might load a .env file.
	systemEnvMode := os.Getenv(EnvXyliumMode)

	// No lock needed here as init() is single-threaded per package.
	if isValidModeInternal(systemEnvMode) {
		currentGlobalMode = systemEnvMode
		modeSource = "env_system_init"
		// Optional: Log this initial system environment detection.
		// log.Printf("[XYLIUM-INIT] Initial mode detected from system environment: '%s'. Source: '%s'.\n", currentGlobalMode, modeSource)
	} else {
		// If no valid system env var, it remains the 'internal_default' (DebugMode).
		// log.Printf("[XYLIUM-INIT] No valid XYLIUM_MODE in system environment. Using internal default: '%s'. Source: '%s'.\n", currentGlobalMode, modeSource)
	}
}

// isValidModeInternal checks if the provided mode string is one of the valid Xylium modes.
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
// an application might have loaded a .env file after Xylium's `init()` ran
// but before the router was created. This function allows that .env value to take effect,
// unless xylium.SetMode() was explicitly called.
func updateGlobalModeFromEnvOnRouterInit() {
	currentGlobalModeLock.Lock()
	defer currentGlobalModeLock.Unlock()

	// Read the current value of XYLIUM_MODE from the environment.
	// This reflects system env vars OR vars loaded by the app from a .env file.
	envModeAtRouterCreation := os.Getenv(EnvXyliumMode)

	// Log for debugging how mode decision is being made at this point.
	// log.Printf("[XYLIUM-ROUTER-INIT] Attempting to update mode from ENV. Found: '%s'. Current Mode: '%s', Source: '%s'.\n",
	// 	envModeAtRouterCreation, currentGlobalMode, modeSource)

	if isValidModeInternal(envModeAtRouterCreation) {
		// Priority:
		// 1. Explicit xylium.SetMode() call ("set_mode_explicit") takes highest precedence.
		// 2. Environment variable read at router creation ("env_router_init").
		// 3. Environment variable read at package init ("env_system_init").
		// 4. Internal default ("internal_default").

		// Only update if the current mode was NOT set by an explicit SetMode() call
		// AND the environment mode is different from the current mode.
		if modeSource != "set_mode_explicit" {
			if currentGlobalMode != envModeAtRouterCreation {
				log.Printf("[XYLIUM-INFO] Xylium mode updated to '%s' from environment at router initialization (was '%s' from '%s').\n",
					envModeAtRouterCreation, currentGlobalMode, modeSource)
				currentGlobalMode = envModeAtRouterCreation
				modeSource = "env_router_init"
			}
			// If currentGlobalMode is already envModeAtRouterCreation (e.g., set by env_system_init), no change needed.
		} else {
			// If SetMode() was called, but ENV is different, SetMode() wins. Log a warning.
			if currentGlobalMode != envModeAtRouterCreation {
				log.Printf("[XYLIUM-WARN] Environment variable XYLIUM_MODE ('%s') differs from explicitly set mode ('%s'). Explicit SetMode() call takes precedence.\n",
					envModeAtRouterCreation, currentGlobalMode)
			}
		}
	} else if envModeAtRouterCreation != "" { // ENV var is set but invalid.
		log.Printf("[XYLIUM-WARN] Invalid XYLIUM_MODE ('%s') in environment at router initialization. Using current mode: '%s' (from '%s').\n",
			envModeAtRouterCreation, currentGlobalMode, modeSource)
	}
	// If envModeAtRouterCreation is empty and invalid, do nothing; retain current mode.
}

// SetMode explicitly sets Xylium's global operating mode.
// This function has the highest precedence and will override any mode set
// by environment variables or defaults.
// It should typically be called early in the application's main function if needed.
// Panics if an invalid modeValue is provided.
func SetMode(modeValue string) {
	currentGlobalModeLock.Lock()
	defer currentGlobalModeLock.Unlock()

	if !isValidModeInternal(modeValue) {
		panic(fmt.Sprintf("xylium: invalid mode '%s' provided to SetMode. Use xylium.DebugMode, xylium.TestMode, or xylium.ReleaseMode.", modeValue))
	}

	// Update only if the mode is changing or if the source was different (e.g., from ENV to explicit).
	if currentGlobalMode != modeValue || modeSource != "set_mode_explicit" {
		log.Printf("[XYLIUM-INFO] Xylium global operating mode explicitly set to '%s' (was '%s' from '%s').\n",
			modeValue, currentGlobalMode, modeSource)
		currentGlobalMode = modeValue
		modeSource = "set_mode_explicit" // Mark that mode was set explicitly.
	}
}

// Mode returns the current global operating mode of Xylium.
// Applications and Xylium components (like the Router) should use this function
// to get the effective operating mode.
func Mode() string {
	currentGlobalModeLock.RLock()
	defer currentGlobalModeLock.RUnlock()
	// Optional: Log when Mode() is called for debugging, can be noisy.
	// log.Printf("[XYLIUM-MODE-ACCESS] Mode() called, returning: '%s' (Source: '%s')\n", currentGlobalMode, modeSource)
	return currentGlobalMode
}
