// examples/unified_showcase.go
package main

import (
	"errors"
	"fmt"
	"net/http" // Diperlukan untuk http.StatusCreated, http.StatusOK, dll.
	"os"
	"path/filepath"
	"strings" // <<< TAMBAHKAN IMPORT INI
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/go-playground/validator/v10" // Tidak perlu di sini jika DefaultValidator cukup
)

// --- Model Data ---
type CreateItemInput struct {
	Name  string `json:"name" validate:"required,min=3,max=50"`
	Value int    `json:"value" validate:"gte=0,lte=1000"`
}

type QueryFilterInput struct {
	Term      string   `query:"term" validate:"omitempty,max=100"`
	Status    []string `query:"status" validate:"omitempty,dive,oneof=active inactive pending"`
	MinRating int      `query:"min_rating" validate:"omitempty,min=1,max=5"`
	IsUrgent  *bool    `query:"is_urgent"` // Pointer untuk membedakan tidak ada vs false
}

// Penyimpanan item dalam memori (sangat sederhana)
var (
	itemsStore    []CreateItemInput
	itemsStoreMux sync.RWMutex
	startupTime   time.Time
)

// --- Middleware Kustom Sederhana ---
func simpleRequestLoggerMiddleware() xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			logger := c.Logger().WithFields(xylium.M{"custom_middleware": "SimpleRequestLogger"})
			logger.Infof("REQ START: %s %s from %s", c.Method(), c.Path(), c.RealIP())
			err := next(c)
			latency := time.Since(startTime)
			logger.Infof("REQ END: %s %s completed in %v, Status: %d",
				c.Method(), c.Path(), latency, c.Ctx.Response.StatusCode())
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
				return xylium.NewHTTPError(http.StatusUnauthorized, "API Key required")
			}
			if key != validKey {
				logger.Warnf("Invalid API Key provided")
				return xylium.NewHTTPError(http.StatusForbidden, "Invalid API Key")
			}
			logger.Info("API Key validated")
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
			return next(c)
		}
	}
}

func main() {
	startupTime = time.Now()
	app := xylium.New()
	appLogger := app.Logger()

	appLogger.Infof("Xylium Unified Showcase starting in '%s' mode", app.CurrentMode())

	sharedRateLimitStore := xylium.NewInMemoryStore(
		xylium.WithCleanupInterval(5*time.Minute),
		xylium.WithLogger(appLogger.WithFields(xylium.M{"component": "SharedRateLimitStore"})),
	)
	app.RegisterCloser(sharedRateLimitStore)

	app.Use(xylium.RequestID())
	app.Use(simpleRequestLoggerMiddleware())
	app.Use(securityHeadersMiddleware())
	app.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
		Timeout: 15 * time.Second,
		Message: "Sorry, the request took too long to process.",
	}))
	app.Use(xylium.Gzip())
	corsConfig := xylium.DefaultCORSConfig
	corsConfig.AllowOrigins = []string{"http://localhost:3000", "https://myfrontend.com"}
	app.Use(xylium.CORSWithConfig(corsConfig))

	csrfSecureValue := false
	if app.CurrentMode() == xylium.ReleaseMode {
		secureTrue := true
		csrfSecureValue = secureTrue
	}
	csrfConfigVar := xylium.DefaultCSRFConfig
	csrfConfigVar.CookieSecure = &csrfSecureValue
	app.Use(xylium.CSRFWithConfig(csrfConfigVar))

	app.Use(xylium.RateLimiter(xylium.RateLimiterConfig{
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimitStore,
		Skip: func(c *xylium.Context) bool {
			return c.Path() == "/health" || strings.HasPrefix(c.Path(), "/static/") // `strings` sekarang terdefinisi
		},
		Message:        "You have made too many requests globally. Please try again later.",
		LoggerForStore: appLogger.WithFields(xylium.M{"component": "GlobalRateLimiterStore"}),
	}))

	app.GET("/", func(c *xylium.Context) error {
		reqID, _ := c.Get(xylium.ContextKeyRequestID)
		return c.JSON(http.StatusOK, xylium.M{
			"message":    "Welcome to Xylium Unified Showcase!",
			"mode":       c.RouterMode(),
			"request_id": reqID,
		})
	})

	app.GET("/health", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, xylium.M{
			"status":    "healthy",
			"timestamp": time.Now().UTC(),
			"uptime":    time.Since(startupTime).String(),
		})
	})

	app.GET("/greet/:name", func(c *xylium.Context) error {
		name := c.Param("name")
		// PERBAIKAN UNTUK QueryParamDefault:
		greeting := c.QueryParam("greeting") // Ambil query param
		if greeting == "" {                  // Jika kosong, set default
			greeting = "Hello"
		}
		return c.String(http.StatusOK, "%s, %s! Welcome to Xylium.", greeting, name)
	})

	app.GET("/filter-items", func(c *xylium.Context) error {
		var filter QueryFilterInput
		if err := c.BindAndValidate(&filter); err != nil {
			c.Logger().Warnf("Failed to bind or validate query filter: %v", err)
			return err
		}
		c.Logger().Infof("Filtering items with: %+v", filter)
		return c.JSON(http.StatusOK, xylium.M{"applied_filters": filter, "results_count": fmt.Sprintf("Placeholder count for term: %s", filter.Term)})
	})

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
		return c.JSON(http.StatusCreated, xylium.M{"message": "Item created", "item": input})
	})

	app.GET("/items", func(c *xylium.Context) error {
		itemsStoreMux.RLock()
		currentItems := make([]CreateItemInput, len(itemsStore))
		copy(currentItems, itemsStore)
		itemsStoreMux.RUnlock()
		return c.JSON(http.StatusOK, currentItems)
	})

	app.GET("/error/custom", func(c *xylium.Context) error {
		return xylium.NewHTTPError(http.StatusPaymentRequired, "This feature requires a subscription.").WithInternal(errors.New("subscription_check_failed_internal_details"))
	})
	app.GET("/error/generic", func(c *xylium.Context) error {
		return errors.New("simulated generic internal error")
	})
	app.GET("/panic", func(c *xylium.Context) error {
		panic("Deliberate panic to demonstrate Xylium's recovery!")
	})

	app.GET("/csrf-token", func(c *xylium.Context) error {
		token, exists := c.Get(csrfConfigVar.ContextTokenKey)
		if !exists {
			return xylium.NewHTTPError(http.StatusInternalServerError, "CSRF token not found in context")
		}
		return c.JSON(http.StatusOK, xylium.M{"csrf_token": token})
	})

	app.POST("/form-protected", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, xylium.M{"status": "CSRF token valid, form processed"})
	})

	apiV1 := app.Group("/api/v1")
	apiV1.Use(apiKeyAuthMiddleware("v1-secret-api-key"))
	{
		apiV1.GET("/info", func(c *xylium.Context) error {
			user, _ := c.Get("authenticated_user")
			return c.JSON(http.StatusOK, xylium.M{
				"api_version": "v1",
				"user":        user,
				"message":     "Welcome to API v1 (protected by API Key)",
			})
		})
	}

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
		Store:          sharedRateLimitStore,
		Message:        "Too many login attempts or admin actions. Please wait.",
		KeyGenerator:   adminRouteKeyGenerator,
	}))
	{
		adminGroup.GET("/dashboard", func(c *xylium.Context) error {
			authUser, _ := c.Get("user")
			return c.JSON(http.StatusOK, xylium.M{"page": "Admin Dashboard", "auth_user": authUser})
		})
	}

	app.GET("/frequent-resource", func(c *xylium.Context) error {
		return c.String(http.StatusOK, "Accessed frequent resource.")
	}, xylium.RateLimiter(xylium.RateLimiterConfig{
		MaxRequests:    2,
		WindowDuration: 5 * time.Second,
		Store:          sharedRateLimitStore,
		Message:        "You are accessing this frequent resource too often.",
	}))

	staticDir := "./public_html_showcase"
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		appLogger.Warnf("Static directory '%s' not found, creating for demo.", staticDir)
		_ = os.Mkdir(staticDir, 0755)
		indexContent := `<!DOCTYPE html><html><head><title>Xylium Static</title></head><body><h1>Xylium Static Content</h1><p><a href="/static/other.html">Other Page</a></p></body></html>`
		_ = os.WriteFile(filepath.Join(staticDir, "index.html"), []byte(indexContent), 0644)
		otherContent := `<!DOCTYPE html><html><head><title>Other Page</title></head><body><h1>Another Static Page</h1><p><a href="/static/">Back to Index</a></p></body></html>`
		_ = os.WriteFile(filepath.Join(staticDir, "other.html"), []byte(otherContent), 0644)
	}
	app.ServeFiles("/static", staticDir)
	appLogger.Infof("Serving static files from '%s' under URL '/static'", staticDir)

	listenAddr := ":8080"
	appLogger.Infof("Server is ready and listening on http://localhost%s", listenAddr)
	if err := app.Start(listenAddr); err != nil {
		appLogger.Fatalf("Failed to start Xylium server: %v", err)
	}

	appLogger.Info("Xylium server has shut down gracefully.")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
