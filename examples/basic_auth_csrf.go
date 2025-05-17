// examples/basic_auth_csrf.go
package main

import (
	"encoding/json" // For manual JSON unmarshalling in PUT handler to check field presence
	"fmt"
	"log"
	"net/http" // For HTTP status constants (e.g., http.StatusOK)
	"os"
	"sync"
	"time"

	// Adjust this import path if your project structure is different or you vendor dependencies.
	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/go-playground/validator/v10"       // For validation error type assertion
	"github.com/valyala/fasthttp"                  // For fasthttp constants like CookieSameSiteLaxMode and CompressBestSpeed
)

// --- Data Model: Task ---
// Task represents a task item in the application.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title" validate:"required,min=3,max=200"`
	Description string     `json:"description,omitempty"`
	Completed   bool       `json:"completed"`
	DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"` // DueDate, if provided, must be in the future.
	Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"` // Each tag, if provided, must be at least 2 chars.
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- In-Memory Data Storage (for demonstration purposes) ---
// In a production application, replace this with a persistent database.
var (
	tasksDB      = make(map[string]Task) // In-memory map to store tasks, keyed by task ID.
	tasksDBLock  sync.RWMutex            // Read-Write mutex to protect concurrent access to tasksDB.
	nextTaskID   = 1                     // Simple integer counter for generating unique task IDs.
	taskIDPrefix = "task-"               // Prefix for generated task IDs for better readability.
)

// generateTaskID creates a unique, prefixed ID for a new task.
func generateTaskID() string {
	id := fmt.Sprintf("%s%d", taskIDPrefix, nextTaskID)
	nextTaskID++
	return id
}

// --- Custom Middleware Implementations ---

// requestLoggerMiddleware logs details of each incoming HTTP request.
// It's beneficial to place this middleware after RequestID to include the request ID in logs.
func requestLoggerMiddleware(logger xylium.Logger) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now() // Record start time to calculate latency.

			// Attempt to retrieve RequestID from the context (set by RequestID middleware).
			requestIDVal, _ := c.Get(xylium.ContextKeyRequestID) // Using the exported constant.
			requestIDStr, _ := requestIDVal.(string)

			err := next(c) // Process the request by calling the next handler in the chain.

			// After the handler has finished, log relevant details.
			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode() // Get HTTP status code from fasthttp's context.

			logMessage := fmt.Sprintf("ClientIP: %s | Method: %s | Path: %s | Status: %d | Latency: %s | UserAgent: \"%s\"",
				c.RealIP(),    // Get the perceived client IP address.
				c.Method(),    // Get the HTTP request method.
				c.Path(),      // Get the request path.
				statusCode,    // Get the HTTP response status code.
				latency,       // Get the request processing duration.
				c.UserAgent(), // Get the client's User-Agent header.
			)

			if requestIDStr != "" {
				logger.Printf("[ReqID: %s] %s", requestIDStr, logMessage)
			} else {
				logger.Printf("%s", logMessage)
			}
			return err // Propagate any error that occurred in the handler chain.
		}
	}
}

// simpleAuthMiddleware provides basic API key authentication.
// It expects an API key in the "X-API-Key" request header.
func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				// Return an HTTPError. Xylium's GlobalErrorHandler will process this.
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API key provided.")
			}
			// If authentication is successful, optionally store information in the context.
			c.Set("authenticated_via", "APIKey")
			return next(c) // Proceed to the next handler in the chain.
		}
	}
}

// --- Global Application Variables ---
var (
	startupTime            time.Time          // Stores the application's startup time for uptime calculation.
	sharedRateLimiterStore xylium.LimiterStore // A shared store for the global rate limiter middleware.
	closeSharedStoreFunc   func() error       // Function to properly close/cleanup the shared rate limiter store on application shutdown.
)

// --- Main Application Entry Point ---
func main() {
	// --- Xylium Operating Mode Configuration (Optional) ---
	// The operating mode can be set programmatically before initializing the router.
	// If not set, Xylium defaults to "release" mode or respects the XYLIUM_MODE environment variable.
	// Example:
	// xylium.SetMode(xylium.DebugMode)
	// xylium.SetMode(xylium.TestMode)

	startupTime = time.Now().UTC()

	// --- Application Logger Setup ---
	// Using Go's standard log package for application-level logging.
	appLogger := log.New(os.Stdout, "[TaskAPIApp-CSRF] ", log.LstdFlags|log.Lshortfile)

	// --- Shared Rate Limiter Store Initialization ---
	// It's good practice to explicitly manage the lifecycle of stateful components like stores.
	// This store is used by the global rate limiter.
	{
		// Create an in-memory store with a periodic cleanup of old entries.
		concreteStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(5 * time.Minute))
		sharedRateLimiterStore = concreteStore
		closeSharedStoreFunc = concreteStore.Close // Assign its Close method for use during shutdown.
	}

	// --- Xylium Server Configuration ---
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger // Use our application logger for Xylium server's internal logging.
	serverCfg.Name = "TaskManagementAPI-CSRF/1.0" // Server name to be used in headers.
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second // Max time for graceful shutdown.

	// --- Xylium Router Initialization ---
	// The router will adopt the operating mode set globally (via SetMode or ENV var).
	router := xylium.NewWithConfig(serverCfg)
	router.Logger().Printf("Application starting in Xylium '%s' mode.", router.CurrentMode())

	// --- Global Middleware Registration (Order of Execution Matters!) ---

	// 1. RequestID Middleware: Adds a unique ID to each request. Essential for tracing and logging.
	router.Use(xylium.RequestID()) // Uses the default "X-Request-ID" header.

	// 2. Request Logger Middleware: Logs details of each request. Placed after RequestID to include the ID in logs.
	router.Use(requestLoggerMiddleware(appLogger)) // Using the application logger.

	// 3. Timeout Middleware: Applies an execution timeout for handlers to prevent long-running requests.
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
		Timeout: 10 * time.Second, // Set a 10-second timeout for requests.
		Message: "The server is taking too long to respond to your request.",
		// Example of a custom error handler for timeouts:
		// ErrorHandler: func(c *xylium.Context, err error) error {
		// 	 c.router.Logger().Printf("Custom timeout handler triggered: %v for path %s", err, c.Path())
		// 	 return c.String(xylium.StatusGatewayTimeout, "Request timed out (custom application message).")
		// },
	}))

	// 4. CORS (Cross-Origin Resource Sharing) Middleware: Manages requests from different domains.
	// Crucial for APIs that are accessed by web frontends hosted on different origins.
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"}, // Specify allowed frontend origins.
		AllowMethods:     []string{xylium.MethodGet, xylium.MethodPost, xylium.MethodPut, xylium.MethodDelete, xylium.MethodOptions, xylium.MethodPatch},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader, "X-CSRF-Token"}, // Include "X-CSRF-Token" for CSRF.
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true, // Set to true if your frontend needs to send credentials (e.g., cookies, Authorization header).
		MaxAge:           3600, // Cache preflight (OPTIONS) request results for 1 hour (3600 seconds).
	}))

	// 5. CSRF (Cross-Site Request Forgery) Protection Middleware:
	// Implements the double submit cookie pattern to protect against CSRF attacks.
	router.Use(xylium.CSRFWithConfig(xylium.CSRFConfig{
		CookieName:     "_csrf_app_token",                // Name of the cookie storing the CSRF token.
		CookieSecure:   false,                            // IMPORTANT: SET TO true IN PRODUCTION (HTTPS)! False for local HTTP development.
		CookieHTTPOnly: false,                            // Must be false if client-side JavaScript needs to read the token from this cookie to send it in a header.
		CookieSameSite: fasthttp.CookieSameSiteLaxMode, // "Lax" provides a good balance of security and usability.
		HeaderName:     "X-CSRF-Token",                 // The HTTP header name expected to contain the token from client AJAX/SPA requests.
		// FormFieldName: "_csrf_form_field",          // If using traditional HTML forms, this would be the name of the hidden input field.
	}))

	// 6. Gzip Compression Middleware: Compresses HTTP response bodies to reduce transfer size and improve speed.
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{
		Level:     fasthttp.CompressBestSpeed, // Prioritize compression speed over ratio.
		MinLength: 1024,                       // Only compress responses larger than 1KB.
	}))

	// 7. Global Rate Limiter Middleware: Provides general rate limiting to protect the server from abuse.
	globalRateLimiterConfig := xylium.RateLimiterConfig{
		MaxRequests:    100,                       // Maximum number of requests allowed...
		WindowDuration: 1 * time.Minute,           // ...within this time window.
		Store:          sharedRateLimiterStore,    // Use the globally shared in-memory store.
		Skip: func(c *xylium.Context) bool { // Function to determine if rate limiting should be skipped for a request.
			return c.Path() == "/health" // Skip rate limiting for the health check endpoint.
		},
		Message: func(c *xylium.Context, limit int, window time.Duration, resetTime time.Time) string {
			// Custom message to be sent when a client is rate-limited.
			ridVal, _ := c.Get(xylium.ContextKeyRequestID)
			rid, _ := ridVal.(string)
			return fmt.Sprintf(
				"[ReqID: %s] Too many requests from IP %s. Limit: %d per %v. Retry after: %s.",
				rid, c.RealIP(), limit, window, resetTime.Format(time.RFC1123),
			)
		},
		SendRateLimitHeaders: xylium.SendHeadersAlways,  // Always send X-RateLimit-* headers in responses.
		RetryAfterMode:       xylium.RetryAfterHTTPDate, // Format the Retry-After header as an HTTP-date.
	}
	router.Use(xylium.RateLimiter(globalRateLimiterConfig))

	// 8. Additional Security Headers Middleware: Sets various HTTP security headers.
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")      // Prevents MIME-sniffing.
			c.SetHeader("X-Frame-Options", "DENY")               // Prevents clickjacking.
			c.SetHeader("X-XSS-Protection", "1; mode=block")    // Enables XSS filtering in older browsers.
			// Consider adding Content-Security-Policy (CSP) for more robust XSS protection.
			// c.SetHeader("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'")
			return next(c)
		}
	})

	// --- Application Route Definitions ---

	// Health Check Endpoint: Useful for load balancers and monitoring.
	router.GET("/health", func(c *xylium.Context) error {
		// Retrieve the CSRF token from the context (set by the CSRF middleware).
		csrfTokenValue, csrfTokenExists := c.Get("csrf_token")
		csrfTokenStr := "" // Default to empty string if not found or not a string.
		if csrfTokenExists {
			csrfTokenStr, _ = csrfTokenValue.(string) // Safely assert to string.
		}

		healthStatus := xylium.M{
			"status":          "healthy",
			"timestamp":       time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":          time.Since(startupTime).String(),
			"xylium_mode":     c.RouterMode(),         // Display current Xylium operating mode.
			"csrf_token_test": csrfTokenStr,           // Include current CSRF token for easy testing/viewing.
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	// CSRF Token Endpoint: Provides the CSRF token to clients (e.g., Single Page Applications).
	// The CSRF middleware automatically sets/renews the token cookie and makes the token
	// available via c.Get("csrf_token").
	router.GET("/csrf-token", func(c *xylium.Context) error {
		tokenVal, _ := c.Get("csrf_token") // The key "csrf_token" is set by Xylium's CSRF middleware.
		tokenStr, _ := tokenVal.(string)
		return c.JSON(http.StatusOK, xylium.M{"csrf_token": tokenStr})
	})

	// Example endpoint for demonstrating query parameter binding and validation.
	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"` // If both present, StartDate must be before EndDate.
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"` // 'dive' validates each element.
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			// BindAndValidate returns an HTTPError; Xylium's GlobalErrorHandler will handle formatting it.
			return err
		}
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	// --- API v1 Route Group ---
	// All routes within this group will be prefixed with "/api/v1".
	apiV1Group := router.Group("/api/v1")
	// Apply API key authentication middleware to all routes in this group.
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	// Tasks API sub-group under /api/v1.
	// CSRF protection (from global middleware) applies to state-changing methods (POST, PUT, DELETE, PATCH) here
	// as they are not in the CSRF middleware's default SafeMethods list.
	tasksAPI := apiV1Group.Group("/tasks")
	{
		// Handler for POST /api/v1/tasks (Create a new task).
		postTaskHandler := func(c *xylium.Context) error {
			var req struct { // Anonymous struct for request binding and validation.
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"` // gt = greater than current time.
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"` // dive validates each tag.
			}
			if err := c.BindAndValidate(&req); err != nil {
				return err // Validation errors are handled by the GlobalErrorHandler.
			}
			// Example of additional custom validation logic.
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				return xylium.NewHTTPError(xylium.StatusBadRequest,
					xylium.M{"due_date": "Due date must be today or in the future."})
			}
			now := time.Now().UTC()
			newTask := Task{
				ID: generateTaskID(), Title: req.Title, Description: req.Description,
				Completed: false, DueDate: req.DueDate, Tags: req.Tags,
				CreatedAt: now, UpdatedAt: now,
			}
			tasksDBLock.Lock() // Obtain write lock before modifying tasksDB.
			tasksDB[newTask.ID] = newTask
			tasksDBLock.Unlock()
			return c.JSON(http.StatusCreated, newTask) // Respond with the newly created task.
		}

		// Apply a specific, stricter rate limiter for the task creation endpoint.
		// This uses a new, independent InMemoryStore to avoid interference with the global limiter.
		createTaskLimiterStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(2 * time.Minute))
		// IMPORTANT: In a real application, you would also need to manage the lifecycle of
		// `createTaskLimiterStore` (i.e., call its Close() method on application shutdown)
		// to prevent goroutine leaks from its cleanup mechanism. This is omitted here for brevity.

		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests:    5,               // Allow only 5 task creations...
			WindowDuration: 1 * time.Minute, // ...per minute, per unique key.
			Store:          createTaskLimiterStore,
			Message:        "You are attempting to create tasks too frequently. Please wait a moment.",
			KeyGenerator: func(c *xylium.Context) string {
				// Generate a rate limit key based on both the client's IP and their API Key for fairer per-user limiting.
				// simpleAuthMiddleware has already run at this point for routes in this group.
				apiKey := c.Header("X-API-Key")
				return "task_create_limit:" + c.RealIP() + ":" + apiKey
			},
			SendRateLimitHeaders: xylium.SendHeadersOnLimit, // Send X-RateLimit-* headers only when the client is actually limited.
		}
		// Apply this route-specific rate limiter middleware ONLY to the POST (create task) endpoint.
		tasksAPI.POST("", postTaskHandler, xylium.RateLimiter(createTaskRateLimiterConfig))

		// GET /api/v1/tasks - List all tasks.
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock() // Use a read lock for concurrent-safe reading.
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB)) // Pre-allocate slice capacity.
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			return c.JSON(http.StatusOK, taskList)
		})

		// GET /api/v1/tasks/:id - Get a specific task by its ID.
		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id") // Retrieve the 'id' route parameter.
			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()
			if !found {
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			return c.JSON(http.StatusOK, task)
		})

		// PUT /api/v1/tasks/:id - Update an existing task (supports partial updates).
		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")

			// To correctly handle partial updates (distinguish "field not sent" from "field sent as null"),
			// first unmarshal the request body to a map of raw JSON messages.
			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body() // Get the raw request body once.
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Invalid JSON body for update (raw parse).").WithInternal(err)
			}

			tasksDBLock.Lock() // Obtain a full write lock for the read-then-update operation.
			defer tasksDBLock.Unlock()

			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for update.", taskID))
			}

			// Bind the request body to a struct with pointers for optional fields.
			// This allows us to see which fields were actually provided in the JSON payload.
			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			// Unmarshal the original bodyBytes again into the struct with pointers.
			if err := json.Unmarshal(bodyBytes, &req); err != nil { 
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Failed to parse JSON into update fields structure.").WithInternal(err)
			}

			// Validate the fields that were provided in the request.
            currentValidator := xylium.GetValidator() // Get Xylium's shared validator instance.
            if err := currentValidator.Struct(&req); err != nil {
                if vErrs, ok := err.(validator.ValidationErrors); ok { // Check for specific validation errors.
                    errFields := make(map[string]string) // Format errors for a user-friendly response.
                    for _, fe := range vErrs {
                        errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v'). Param: %s.", fe.Tag(), fe.Value(), fe.Param())
                    }
                    return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed during update.", "details": errFields}).WithInternal(err)
                }
                // Handle other (non-validation) errors from the validator.
                return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error during update.").WithInternal(err)
            }

			changed := false // Flag to track if any modifications were made.
			if req.Title != nil { // If Title was provided in the JSON (even if it's an empty string after unmarshalling).
				existingTask.Title = *req.Title
				changed = true
			}
			if req.Description != nil {
				existingTask.Description = *req.Description
				changed = true
			}
			if req.Completed != nil {
				existingTask.Completed = *req.Completed
				changed = true
			}
			// Check if 'due_date' was explicitly present in the request payload using the rawRequest map.
			// This allows distinguishing between "due_date not sent" and "due_date: null".
			if _, dueDateInRequest := rawRequest["due_date"]; dueDateInRequest {
				if req.DueDate == nil { // 'due_date' was present and explicitly set to null.
					existingTask.DueDate = nil
				} else { // 'due_date' was present and had a value.
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(xylium.StatusBadRequest,
							xylium.M{"due_date": "Due date must be today or in the future if provided for update."})
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}
			// Check if 'tags' was explicitly present in the request payload.
			if _, tagsInRequest := rawRequest["tags"]; tagsInRequest {
				if req.Tags == nil { // 'tags' was present and explicitly set to null.
					existingTask.Tags = nil
				} else { // 'tags' was present and had a value (could be an empty array []).
					existingTask.Tags = *req.Tags
				}
				changed = true
			}

			if changed { // If any field was modified, update the timestamp and save.
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask // Update the task in our in-memory "database".
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		// DELETE /api/v1/tasks/:id - Delete a task by its ID.
		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock() // Obtain write lock.
			_, found := tasksDB[taskID]
			if found {
				delete(tasksDB, taskID) // Remove the task from the map.
			}
			tasksDBLock.Unlock()
			if !found {
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID))
			}
			return c.NoContent(http.StatusNoContent) // Standard HTTP 204 No Content response for successful deletion.
		})

		// PATCH /api/v1/tasks/:id/complete - Mark a task as complete.
		// PATCH is suitable for partial updates to a resource, like changing a single flag.
		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock() // Obtain write lock.
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(xylium.StatusNotFound, "Task not found for completion.")
			}
			if task.Completed { // If the task is already complete, the operation is idempotent.
				return c.JSON(http.StatusOK, task) // Return the current state.
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task // Save the updated task.
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Admin Area Route Group (Protected by Basic Authentication) ---
	adminGroup := router.Group("/admin")
	// Define the Basic Authentication validator function.
	basicAuthValidator := func(username, password string, c *xylium.Context) (user interface{}, valid bool, err error) {
		// In a real application, validate credentials against a secure store (e.g., database, LDAP).
		// Use cryptographically hashed passwords.
		if username == "admin" && password == "s3cr3tP@sswOrd" {
			// If valid, return user information (can be any struct or map) and true.
			// This user info will be available in c.Get("user") in subsequent handlers.
			return xylium.M{"username": username, "role": "administrator", "auth_time": time.Now().Format(time.RFC3339)}, true, nil
		}
		return nil, false, nil // Credentials are not valid.
	}
	// Apply BasicAuth middleware to the admin group.
	adminGroup.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{
		Validator: basicAuthValidator,
		Realm:     "Secure Admin Area", // Realm string displayed in the browser's authentication dialog.
	}))
	{ // Inner scope for admin routes.
		// GET /admin/dashboard - Example admin dashboard endpoint.
		adminGroup.GET("/dashboard", func(c *xylium.Context) error {
			userVal, _ := c.Get("user") // The "user" key is set by the BasicAuth middleware upon successful authentication.
			return c.JSON(http.StatusOK, xylium.M{
				"message":        "Welcome to the Admin Dashboard!",
				"user_info":      userVal,
				"xylium_mode_is": c.RouterMode(), // Display the current Xylium operating mode.
			})
		})
		// POST /admin/settings - Example admin action that modifies state.
		adminGroup.POST("/settings", func(c *xylium.Context) error {
			// CSRF token (from global CSRF middleware) will be checked here as it's a POST request.
			// Ensure your client sends the X-CSRF-Token header for this request.
			return c.JSON(http.StatusOK, xylium.M{"message": "Admin settings updated successfully."})
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	// The logger (appLogger, which is router.Logger()) will print the server's operating mode
	// during startup if the server start functions (e.g., ListenAndServeGracefully) include this logging.
	router.Logger().Printf("Task API server with CSRF and Basic Auth starting. Listening on http://localhost%s", listenAddr)

	// Use ListenAndServeGracefully to enable graceful shutdown on OS signals (SIGINT, SIGTERM).
	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		// Use the initial appLogger for fatal errors, as router.Logger() might not be
		// fully initialized if the server fails very early.
		appLogger.Fatalf("FATAL: API server encountered an error: %v", err)
	}

	// --- Cleanup Shared Resources on Shutdown ---
	// This code runs after ListenAndServeGracefully has completed (i.e., server has shut down).
	if closeSharedStoreFunc != nil {
		appLogger.Println("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appLogger.Printf("Error closing shared rate limiter store: %v", err)
		}
	}
	// Note: The `createTaskLimiterStore.Close()` method for the task-specific rate limiter
	// is not explicitly managed in this example for brevity. In a production application,
	// all such stateful resources should have well-defined cleanup mechanisms.

	appLogger.Println("Task API server has shut down gracefully.")
} // End of main function
