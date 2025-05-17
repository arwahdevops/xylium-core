// src/xylium/mode.go
package xylium

import (
	"fmt"
	"log" // Standard log for internal framework messages.
	"os"
	"sync" // For protecting currentGlobalMode if accessed concurrently (though SetMode is usually early).
)

const (
	// DebugMode indicates Xylium's debug mode.
	DebugMode string = "debug"
	// TestMode indicates Xylium's test mode.
	TestMode string = "test"
	// ReleaseMode indicates Xylium's release (production) mode.
	ReleaseMode string = "release"
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

var (
	currentGlobalMode     string
	currentGlobalModeLock sync.RWMutex
	modeInitialized       bool // Flag to indicate if initial mode setting (from ENV or default) has occurred.
	routerInstanceCreated bool // Flag to indicate if a router instance has been made.
)

// init function is called when the xylium package is first imported.
// It performs an *initial* read of the XYLIUM_MODE environment variable.
// The mode can be explicitly set or overridden later by calling xylium.SetMode().
func init() {
	initialMode := os.Getenv(EnvXyliumMode)
	resolvedMode := ReleaseMode // Default to ReleaseMode

	if initialMode != "" {
		switch initialMode {
		case DebugMode, TestMode, ReleaseMode:
			resolvedMode = initialMode
		default:
			// Use standard log as the framework's logger might not be available yet.
			log.Printf("[XYLIUM-WARN] Invalid initial mode '%s' specified in %s. Defaulting to '%s'. Valid modes: %s, %s, %s.\n",
				initialMode, EnvXyliumMode, resolvedMode, DebugMode, TestMode, ReleaseMode)
		}
	}
	// Set the initial global mode. No lock needed here as init() is single-threaded per package.
	currentGlobalMode = resolvedMode
	modeInitialized = true
	// Log initial discovery (optional, can be verbose if Xylium is just a library)
	// log.Printf("[XYLIUM-INFO] Initial global mode detected as '%s' (from ENV or default).\n", currentGlobalMode)
}

// SetMode sets Xylium's global operating mode.
// This function is the definitive way for an application to set the desired mode,
// typically called early in the application's main function, especially after loading
// configurations like .env files.
// If called after a Xylium router instance has been created, a warning will be logged,
// as router instances adopt the mode prevalent at their creation time.
func SetMode(modeValue string) {
	currentGlobalModeLock.Lock()
	defer currentGlobalModeLock.Unlock()

	validMode := false
	switch modeValue {
	case DebugMode, TestMode, ReleaseMode:
		validMode = true
	default:
		// Panic for invalid mode to ensure correct configuration.
		panic(fmt.Sprintf("xylium: invalid mode '%s' provided to SetMode. Use xylium.DebugMode, xylium.TestMode, or xylium.ReleaseMode.", modeValue))
	}

	if validMode {
		if currentGlobalMode != modeValue {
			// Log the change if it's different from the current mode.
			// This log can use the standard 'log' package as it's a framework-level setting.
			log.Printf("[XYLIUM-INFO] Xylium global operating mode changed from '%s' to '%s'.\n", currentGlobalMode, modeValue)
			currentGlobalMode = modeValue
		} else if !modeInitialized {
			// If modeInitialized was somehow false and SetMode is the first to set it.
			currentGlobalMode = modeValue
			log.Printf("[XYLIUM-INFO] Xylium global operating mode initialized to '%s'.\n", currentGlobalMode)
		}


		// Warning if mode is set after a router instance has already been created.
		// The `routerInstanceCreated` flag would be set by `xylium.New` or `xylium.NewWithConfig`.
		if routerInstanceCreated {
			log.Printf("[XYLIUM-WARN] SetMode(\"%s\") called after a Xylium router instance has been created. Existing router instances will not adopt this new mode. New router instances will use this mode: '%s'.\n", modeValue, currentGlobalMode)
		}
	}
	modeInitialized = true // Ensure it's marked as initialized.
}

// Mode returns the current global operating mode of Xylium.
// Applications and Xylium components (like the Router) should use this function
// to get the effective operating mode.
func Mode() string {
	currentGlobalModeLock.RLock()
	defer currentGlobalModeLock.RUnlock()
	if !modeInitialized {
		// This case should ideally not be reached if init() runs or SetMode() is called.
		// Fallback to ensure a mode is always returned.
		log.Println("[XYLIUM-WARN] Mode() called before mode was explicitly initialized or set; defaulting to ReleaseMode for this call.")
		return ReleaseMode
	}
	return currentGlobalMode
}

// notifyRouterCreated is an internal function called by Router's initialization
// to signal that at least one router instance exists. This is used by SetMode
// to issue a warning if the mode is changed after router creation.
func notifyRouterCreated() {
	currentGlobalModeLock.Lock() // Protect write to routerInstanceCreated
	routerInstanceCreated = true
	currentGlobalModeLock.Unlock()
}
