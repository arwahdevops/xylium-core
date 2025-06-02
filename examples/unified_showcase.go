// examples/unified_showcase.go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/go-playground/validator/v10" // Not needed here if DefaultValidator is sufficient
)

// --- Data Models ---
type CreateItemInput struct {
	Name  string `json:"name" validate:"required,min=3,max=50"`
	Value int    `json:"value" validate:"gte=0,lte=1000"`
}

type QueryFilterInput struct {
	Term      string   `query:"term" validate:"omitempty,max=100"`
	Status    []string `query:"status" validate:"omitempty,dive,oneof=active inactive pending"`
	MinRating int      `query:"min_rating" validate:"omitempty,min=1,max=5"`
	IsUrgent  *bool    `query:"is_urgent"` // Pointer to distinguish not present vs. false
}

// In-memory item storage (very simple for demo)
var (
	itemsStore    []CreateItemInput
	itemsStoreMux sync.RWMutex
	startupTime   time.Time
)

// --- Simple Custom Middleware ---
func simpleRequestLoggerMiddleware() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			// Logger from context, potentially enriched by RequestID middleware
			logger := c.Logger().WithFields(xylium.M{"custom_middleware": "SimpleRequestLogger"})
			logger.Infof("REQ START: %s %s from %s", c.Method(), c.Path(), c.RealIP())

			err := next(c) // Call the next handler

			latency := time.Since(startTime)
			// Ensure c.Ctx is not nil before accessing response details,
			// especially if an early panic could have occurred before c.Ctx was fully handled.
			statusCode := 0
			if c.Ctx != nil {
				statusCode = c.Ctx.Response.StatusCode()
			}

			logger.Infof("REQ END: %s %s completed in %v, Status: %d",
				c.Method(), c.Path(), latency, statusCode)
			return err
		}
	}
}

func apiKeyAuthMiddleware(validKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			logger := c.Logger().WithFields(xylium.M{"middleware": "APIKeyAuth"})
			key := c.Header("X-API-Key")
			if key == "" {
				logger.Warn("API Key missing")
				// Using Xylium's status constants
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API Key required")
			}
			if key != validKey {
				logger.Warnf("Invalid API Key provided")
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API Key")
			}
			logger.Info("API Key validated")
			// Store some user identifier based on the API key
			c.Set("authenticated_user", "api_user_"+key[:min(len(key), 5)])
			return next(c)
		}
	}
}

func securityHeadersMiddleware() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			// Consider adding other headers like:
			// c.SetHeader("Content-Security-Policy", "default-src 'self'")
			// c.SetHeader("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			return next(c)
		}
	}
}

func main() {
	startupTime = time.Now()

	// Initialize Xylium. Default mode is DebugMode unless XYLIUM_MODE env var is set.
	// To set programmatically: xylium.SetMode(xylium.ReleaseMode) // before xylium.New()
	app := xylium.New()
	appLogger := app.Logger() // Application-level logger

	appLogger.Infof("Xylium Unified Showcase starting in '%s' mode", app.CurrentMode())

	// Create a shared rate limiter store and register it for graceful shutdown
	sharedRateLimitStore := xylium.NewInMemoryStore(
		xylium.WithCleanupInterval(5*time.Minute),                                              // Optional: custom cleanup interval
		xylium.WithLogger(appLogger.WithFields(xylium.M{"component": "SharedRateLimitStore"})), // Optional: provide logger
	)
	app.RegisterCloser(sharedRateLimitStore) // Xylium will call store.Close() on shutdown

	// --- Global Middleware Setup ---
	app.Use(xylium.RequestID())                            // Adds a unique request ID
	app.Use(simpleRequestLoggerMiddleware())               // Custom request logger
	app.Use(securityHeadersMiddleware())                   // Adds common security headers
	app.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{ // Request timeout
		Timeout: 15 * time.Second,
		Message: "Sorry, the request took too long to process.",
	}))
	app.Use(xylium.Gzip()) // Enables Gzip compression for eligible responses

	// CORS Configuration (Example: allow specific origins)
	corsConfig := xylium.DefaultCORSConfig
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "https://myfrontend.com"} // Explicitly list allowed origins
	// corsConfig.AllowCredentials = true // If your frontend sends credentials (e.g., cookies)
	app.Use(xylium.CORSWithConfig(corsConfig))

	// CSRF Protection Configuration
	// Default CookieSecure is true. Set to false for local HTTP development if needed.
	csrfSecureCookie := app.CurrentMode() == xylium.ReleaseMode // True in release, false otherwise (for local HTTP)
	csrfConfig := xylium.DefaultCSRFConfig
	csrfConfig.CookieSecure = &csrfSecureCookie
	// If your SPA needs to read the CSRF cookie:
	// httpOnlyFalse := false
	// csrfConfig.CookieHTTPOnly = &httpOnlyFalse
	// csrfConfig.HeaderName = "X-XSRF-TOKEN" // Common for SPAs
	app.Use(xylium.CSRFWithConfig(csrfConfig))

	// Global Rate Limiter (example: 100 requests per minute per IP)
	app.Use(xylium.RateLimiter(xylium.RateLimiterConfig{
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimitStore, // Use the shared store
		Skip: func(c *xylium.Context) bool { // Optional: skip rate limiting for certain paths
			return c.Path() == "/health" || strings.HasPrefix(c.Path(), "/static/")
		},
		Message: "You have made too many requests globally. Please try again later.",
		// LoggerForStore is already set when sharedRateLimitStore was created.
	}))

	// --- Basic Routes ---
	app.GET("/", func(c *xylium.Context) error {
		reqID, _ := c.Get(xylium.ContextKeyRequestID)
		return c.JSON(xylium.StatusOK, xylium.M{
			"message":    "Welcome to Xylium Unified Showcase!",
			"mode":       c.RouterMode(),
			"request_id": reqID,
		})
	})

	app.GET("/health", func(c *xylium.Context) error {
		return c.JSON(xylium.StatusOK, xylium.M{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
			"uptime":    time.Since(startupTime).String(),
		})
	})

	// --- Route with Path and Query Parameters ---
	app.GET("/greet/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		// Corrected: Manually handle default for query parameter as QueryParamDefault(key, default) string does not exist
		greeting := c.QueryParam("greeting")
		if greeting == "" {
			greeting = "Hello" // Default greeting
		}
		return c.String(xylium.StatusOK, "%s, %s! Welcome to Xylium.", greeting, name)
	})

	// --- Binding and Validation for Query Parameters ---
	app.GET("/filter-items", func(c *xylium.Context) error {
		var filter QueryFilterInput
		if err := c.BindAndValidate(&filter); err != nil {
			c.Logger().Warnf("Failed to bind or validate query filter: %v", err)
			return err // Let GlobalErrorHandler handle the *xylium.HTTPError
		}
		c.Logger().Infof("Filtering items with: %+v", filter)
		return c.JSON(xylium.StatusOK, xylium.M{"applied_filters": filter, "results_count": fmt.Sprintf("Placeholder count for term: %s", filter.Term)})
	})

	// --- Binding and Validation for JSON Body ---
	app.POST("/items", func(c *xylium.Context) error {
		var input CreateItemInput
		if err := c.BindAndValidate(&input); err != nil {
			c.Logger().Warnf("Failed to bind or validate item: %v", err)
			return err
		}
		itemsStoreMux.Lock()
		itemsStore = append(itemsStore, input)
		itemsStoreMux.Unlock()
		c.Logger().Infof("New item created: %s", input.Name)
		return c.JSON(xylium.StatusCreated, xylium.M{"message": "Item created", "item": input})
	})

	app.GET("/items", func(c *xylium.Context) error {
		itemsStoreMux.RLock()
		currentItems := make([]CreateItemInput, len(itemsStore))
		copy(currentItems, itemsStore)
		itemsStoreMux.RUnlock()
		return c.JSON(xylium.StatusOK, currentItems)
	})

	// --- Error Handling Demonstrations ---
	app.GET("/error/custom", func(c *xylium.Context) error {
		// Return a specific HTTPError
		return xylium.NewHTTPError(xylium.StatusPaymentRequired, "This feature requires a subscription.").WithInternal(errors.New("subscription_check_failed_internal_details"))
	})
	app.GET("/error/generic", func(c *xylium.Context) error {
		// Return a generic Go error (will be treated as 500 Internal Server Error)
		return errors.New("simulated generic internal error")
	})
	app.GET("/panic", func(c *xylium.Context) error {
		// Demonstrate panic recovery
		panic("Deliberate panic to demonstrate Xylium's recovery!")
	})

	// --- CSRF Token Route (for SPAs or manual form integration) ---
	app.GET("/csrf-token", func(c *xylium.Context) error {
		token, exists := c.Get(csrfConfig.ContextTokenKey) // Use the key from the config used
		if !exists {
			return xylium.NewHTTPError(xylium.StatusInternalServerError, "CSRF token not found in context")
		}
		return c.JSON(xylium.StatusOK, xylium.M{"csrf_token": token})
	})

	// Example of a route protected by CSRF (for unsafe methods like POST)
	app.POST("/form-protected", func(c *xylium.Context) error {
		// CSRF middleware will validate the token before this handler is reached.
		// If validation fails, the middleware's error handler is invoked.
		return c.JSON(xylium.StatusOK, xylium.M{"status": "CSRF token valid, form processed"})
	})

	// --- Route Grouping & Group-Specific Middleware ---
	// API v1 group with API Key Authentication
	apiV1 := app.Group("/api/v1")
	apiV1.Use(apiKeyAuthMiddleware("v1-secret-api-key")) // Apply API key auth to this group
	{
		apiV1.GET("/info", func(c *xylium.Context) error {
			user, _ := c.Get("authenticated_user")
			return c.JSON(xylium.StatusOK, xylium.M{
				"api_version": "v1",
				"user":        user,
				"message":     "Welcome to API v1 (protected by API Key)",
			})
		})
	}

	// Admin group with BasicAuth and a separate RateLimiter
	adminRouteKeyGenerator := func(c *xylium.Context) string { return "admin_route_limit:" + c.RealIP() }
	adminGroup := app.Group("/admin")
	adminGroup.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{
		Validator: func(username, password string, c *xylium.Context) (interface{}, bool, error) {
			if username == "admin" && password == "securePass123" {
				return map[string]string{"username": username, "role": "administrator"}, true, nil
			}
			return nil, false, nil
		},
		Realm: "Admin Section",
	}))
	adminGroup.Use(xylium.RateLimiter(xylium.RateLimiterConfig{
		MaxRequests:    5,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimitStore, // Can use the same shared store or a new one
		Message:        "Too many login attempts or admin actions. Please wait.",
		KeyGenerator:   adminRouteKeyGenerator, // Custom key for admin routes
	}))
	{
		adminGroup.GET("/dashboard", func(c *xylium.Context) error {
			authUser, _ := c.Get("user") // "user" is the default ContextUserKey for BasicAuth
			return c.JSON(xylium.StatusOK, xylium.M{"page": "Admin Dashboard", "auth_user": authUser})
		})
	}

	// --- Route-Specific Middleware (Rate Limiter) ---
	app.GET("/frequent-resource", func(c *xylium.Context) error {
		return c.String(xylium.StatusOK, "Accessed frequent resource.")
	}, xylium.RateLimiter(xylium.RateLimiterConfig{ // Middleware applied only to this route
		MaxRequests:    2,
		WindowDuration: 5 * time.Second,
		Store:          sharedRateLimitStore,
		Message:        "You are accessing this frequent resource too often.",
		// KeyGenerator defaults to c.RealIP()
	}))

	// --- Static File Serving ---
	staticDir := "./public_html_showcase"
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		appLogger.Warnf("Static directory '%s' not found, creating for demo.", staticDir)
		_ = os.Mkdir(staticDir, 0755)
		indexContent := `<!DOCTYPE html><html><head><title>Xylium Static</title></head><body><h1>Xylium Static Content</h1><p><a href="/static/other.html">Other Page</a></p></body></html>`
		_ = os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(indexContent), 0644)
		otherContent := `<!DOCTYPE html><html><head><title>Other Page</title></head><body><h1>Another Static Page</h1><p><a href="/static/">Back to Index</a></p></body></html>`
		_ = os.WriteFile(filepath.Join(staticDir, "other.html"), []byte(otherContent), 0644)
	}
	// Serve files from "./public_html_showcase" under the URL "/static"
	app.ServeFiles("/static", staticDir)
	appLogger.Infof("Serving static files from '%s' under URL '/static'", staticDir)

	// --- Start Server ---
	listenAddr := ":8080"
	appLogger.Infof("Server is ready and listening on http://localhost%s", listenAddr)
	// app.Start() includes graceful shutdown
	if err := app.Start(listenAddr); err != nil {
		appLogger.Fatalf("Failed to start Xylium server: %v", err)
	}

	appLogger.Info("Xylium server has shut down gracefully.")
}

// min helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
