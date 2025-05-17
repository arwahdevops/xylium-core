// src/xylium/mode.go
package xylium

import (
	"fmt"
	"log" // For internal warning logging if necessary
	"os"
)

const (
	// DebugMode indicates Xylium's debug mode.
	// Enables more verbose logging and potentially detailed error information.
	DebugMode string = "debug"
	// TestMode indicates Xylium's test mode.
	// Useful when running test suites, may suppress certain outputs.
	TestMode string = "test"
	// ReleaseMode indicates Xylium's release (production) mode.
	// This is the default mode, featuring minimal logging and full optimizations.
	ReleaseMode string = "release"
)

// EnvXyliumMode is the name of the environment variable used to set Xylium's_Preferring "operating mode".
// "Xylium's mode" is a bit informal._ operating mode.
const EnvXyliumMode = "XYLIUM_MODE"

// currentGlobalMode stores the active global operating mode.
// It defaults to ReleaseMode.
var (
	currentGlobalMode = ReleaseMode
	// disableModeChange is a flag to indicate if the global mode has been "locked"
	// (typically after the first router instance is initialized).
	// Subsequent calls to SetMode will trigger a warning.
	disableModeChange bool = false
)

// init function is called when the xylium package is first imported.
// It reads the XYLIUM_MODE environment variable to set the initial global mode.
// This allows setting the mode via ENV before any application code (like SetMode() or New()) runs.
func init() {
	envMode := os.Getenv(EnvXyliumMode)
	if envMode != "" {
		switch envMode {
		case DebugMode, TestMode, ReleaseMode:
			currentGlobalMode = envMode
			// For initial setup, a log message here might be too early if no logger is configured yet.
			// If needed, a very basic log.Printf could be used, or this info could be logged by the first router.
			// log.Printf("[XYLIUM-INFO] Global mode set to '%s' from environment variable %s\n", currentGlobalMode, EnvXyliumMode)
		default:
			// Use standard log as the framework's logger might not be available yet.
			log.Printf("[XYLIUM-WARN] Invalid mode '%s' specified in %s. Defaulting to '%s'. Valid modes: %s, %s, %s.\n",
				envMode, EnvXyliumMode, currentGlobalMode, DebugMode, TestMode, ReleaseMode)
		}
	}
}

// SetMode sets Xylium's global operating mode.
// This function should ideally be called *before* creating any Router instances
// (e.g., Xylium.New() or Xylium.NewWithConfig()).
// Calling it after the first router has been initialized will print a warning,
// as not all components might pick up this mode change retroactively.
// Valid modes are xylium.DebugMode, xylium.TestMode, and xylium.ReleaseMode.
func SetMode(modeValue string) {
	if disableModeChange {
		// Use standard log for this global setting warning.
		log.Printf("[XYLIUM-WARN] SetMode(\"%s\") called after a Xylium router instance has been initialized. The new mode may not be fully effective for existing or future instances if they rely on the global mode at their initialization time.\n", modeValue)
	}

	switch modeValue {
	case DebugMode, TestMode, ReleaseMode:
		currentGlobalMode = modeValue
	default:
		panic(fmt.Sprintf("xylium: invalid mode: '%s'. Use xylium.DebugMode, xylium.TestMode, or xylium.ReleaseMode.", modeValue))
	}
}

// Mode returns the current global operating mode of Xylium.
// Applications can use this to implement conditional behavior.
func Mode() string {
	return currentGlobalMode
}

// lockModeChanges is an internal function called by the Router's initialization
// to signal that subsequent global mode changes should be logged with a warning.
func lockModeChanges() {
	disableModeChange = true
}
