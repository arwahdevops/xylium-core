// examples/complete.go
package main

import (
	"encoding/json" // For manual JSON unmarshalling (e.g., in the PUT handler)
	"fmt"           // For string formatting
	"log"           // For standard application logging
	"net/http"      // For HTTP status constants
	"os"            // For OS interaction (e.g., Stdout for the logger)
	"sync"          // For synchronization primitives (RWMutex)
	"time"          // For time management

	"github.com/arwahdevops/xylium-core/src/xylium" // Import the Xylium framework
	"github.com/go-playground/validator/v10"       // For struct validation
	"github.com/valyala/fasthttp"                  // For Gzip compression level constants
)

// --- Data Model: Task ---
// Task represents a to-do item in the application.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title" validate:"required,min=3,max=200"`
	Description string     `json:"description,omitempty"`
	Completed   bool       `json:"completed"`
	DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"` // If provided, DueDate must be in the future.
	Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"` // If provided, each tag must be at least 2 chars.
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- In-Memory Data Storage (for demonstration) ---
// For simplicity, this example uses an in-memory map.
// In a production application, you would use a persistent database.
var (
	tasksDB      = make(map[string]Task) // In-memory "database" for tasks.
	tasksDBLock  sync.RWMutex            // Mutex to protect concurrent access to tasksDB.
	nextTaskID   = 1                     // Counter for the next task ID.
	taskIDPrefix = "task-"               // Prefix for generated task IDs.
)

// generateTaskID generates a unique ID for a new task.
func generateTaskID() string {
	id := fmt.Sprintf("%s%d", taskIDPrefix, nextTaskID)
	nextTaskID++
	return id
}

// --- Custom Middleware ---

// requestLoggerMiddleware is a custom middleware to log details of each request.
// It's generally good practice to place this after the RequestID middleware
// so that the request ID can be included in the log entry.
func requestLoggerMiddleware(logger xylium.Logger) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now() // Record request start time.

			// Process the request by calling the next handler/middleware in the chain.
			err := next(c)
			latency := time.Since(startTime)          // Calculate request latency.
			statusCode := c.Ctx.Response.StatusCode() // Get response status code.

			// Retrieve Request ID from context (set by RequestID middleware).
			requestIDVal, _ := c.Get(xylium.ContextKeyRequestID)
			requestIDStr, _ := requestIDVal.(string)

			logMessage := fmt.Sprintf("ClientIP: %s | Method: %s | Path: %s | Status: %d | Latency: %s | UserAgent: \"%s\"",
				c.RealIP(),
				c.Method(),
				c.Path(),
				statusCode,
				latency,
				c.UserAgent(),
			)

			if requestIDStr != "" {
				logger.Printf("[ReqID: %s] %s", requestIDStr, logMessage)
			} else {
				logger.Printf("%s", logMessage)
			}
			return err // Propagate any errors from the handler chain.
		}
	}
}

// simpleAuthMiddleware is a custom middleware for basic API Key authentication.
// It checks for an "X-API-Key" header in the request.
func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				// Return an HTTPError, which will be handled by Xylium's GlobalErrorHandler.
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API key provided.")
			}
			// Optionally store authentication information in the context.
			c.Set("authenticated_via", "APIKey")
			return next(c) // Proceed to the next handler if authentication is successful.
		}
	}
}

// --- Global Application Variables ---
var (
	startupTime            time.Time          // Stores the application's startup time.
	sharedRateLimiterStore xylium.LimiterStore // Shared store for the global rate limiter.
	closeSharedStoreFunc   func() error       // Function to close the shared rate limiter store on shutdown.
)

// --- Main Application Entry Point ---
func main() {
	// --- Xylium Operating Mode Configuration (Optional) ---
	// Set the mode programmatically if needed. Defaults to "release" or XYLIUM_MODE env var.
	// Example:
	// xylium.SetMode(xylium.DebugMode)

	startupTime = time.Now().UTC()

	// --- Application Logger Setup ---
	appLogger := log.New(os.Stdout, "[TaskAPIApp-Complete] ", log.LstdFlags|log.Lshortfile)

	// --- Shared Rate Limiter Store Initialization ---
	// This store is used for the global rate limiter.
	// Its lifecycle (creation and closing) is managed here in main.
	{
		concreteStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(5 * time.Minute))
		sharedRateLimiterStore = concreteStore
		closeSharedStoreFunc = concreteStore.Close // Assign its Close method for deferred execution.
	}

	// --- Xylium Server Configuration ---
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger // Use our application logger for Xylium's server.
	serverCfg.Name = "TaskManagementAPI-Complete/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second // For graceful shutdown.

	// --- Xylium Router Initialization ---
	router := xylium.NewWithConfig(serverCfg)
	// Log the current operating mode.
	router.Logger().Printf("Application starting in Xylium '%s' mode.", router.CurrentMode())

	// --- Global Middleware Registration (Order of Execution is Important!) ---
	// Middleware is executed in the order it's added (outermost to innermost).

	// 1. RequestID: Adds a unique ID to each request. Essential for tracing and logging.
	router.Use(xylium.RequestID()) // Uses default "X-Request-ID" header.

	// 2. Request Logger: Logs details of each request. Placed after RequestID to include the ID.
	router.Use(requestLoggerMiddleware(appLogger)) // Using the application logger.

	// 3. Timeout: Applies an execution timeout for handlers.
	// If placed here, initial logging from RequestLogger will still occur even if a request times out.
	// The timeout error will be handled by the Timeout middleware's error handler or Xylium's GlobalErrorHandler.
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
		Timeout: 5 * time.Second, // Set a 5-second timeout for requests.
		Message: "The server is taking too long to respond to your request.",
		// Custom ErrorHandler example (optional):
		// ErrorHandler: func(c *xylium.Context, err error) error {
		// 	 c.router.Logger().Printf("Custom timeout handler triggered: %v for path %s", err, c.Path())
		// 	 return c.String(xylium.StatusGatewayTimeout, "Request timed out (custom application message).")
		// },
	}))

	// 4. CORS (Cross-Origin Resource Sharing): Manages requests from different domains.
	// Crucial for APIs accessed by web frontends.
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"}, // Specify allowed origins.
		AllowMethods:     []string{xylium.MethodGet, xylium.MethodPost, xylium.MethodPut, xylium.MethodDelete, xylium.MethodOptions, xylium.MethodPatch},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader},
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true, // Important if your frontend sends credentials (cookies, auth headers).
		MaxAge:           3600, // Cache preflight (OPTIONS) request for 1 hour.
	}))

	// 5. Gzip Compression: Compresses HTTP response bodies to reduce transfer size.
	// Placed after main handlers so it compresses the final response.
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{
		Level:     fasthttp.CompressBestSpeed, // Prioritize compression speed.
		MinLength: 1024,                       // Only compress if body > 1KB.
		// ContentTypes: []string{"application/json", "text/html"}, // Example: custom content types to compress.
	}))

	// 6. Global Rate Limiter: General rate limiting to protect the server from excessive requests.
	globalRateLimiterConfig := xylium.RateLimiterConfig{
		MaxRequests:    100,                       // Max requests per window per key.
		WindowDuration: 1 * time.Minute,           // Time window for rate limiting.
		Store:          sharedRateLimiterStore,    // Use the shared, managed store.
		Skip: func(c *xylium.Context) bool { // Skip rate limiting for the /health endpoint.
			return c.Path() == "/health"
		},
		Message: func(c *xylium.Context, limit int, window time.Duration, resetTime time.Time) string {
			// Custom message for rate-limited responses.
			ridVal, _ := c.Get(xylium.ContextKeyRequestID)
			rid, _ := ridVal.(string)
			return fmt.Sprintf(
				"[ReqID: %s] Too many requests from IP %s. Limit: %d per %v. Retry after: %s.",
				rid, c.RealIP(), limit, window, resetTime.Format(time.RFC1123),
			)
		},
		SendRateLimitHeaders: xylium.SendHeadersAlways,  // Always send X-RateLimit-* headers.
		RetryAfterMode:       xylium.RetryAfterHTTPDate, // Format Retry-After header as an HTTP-date string.
	}
	router.Use(xylium.RateLimiter(globalRateLimiterConfig))

	// 7. Additional Security Headers Middleware: Sets common HTTP security headers.
	// Typically one of the last middleware before application handlers.
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			// Potentially add "Content-Security-Policy" here.
			return next(c)
		}
	})

	// --- Application Route Definitions ---

	// Health Check Endpoint.
	router.GET("/health", func(c *xylium.Context) error {
		healthStatus := xylium.M{
			"status":      "healthy",
			"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":      time.Since(startupTime).String(),
			"xylium_mode": c.RouterMode(), // Display current Xylium operating mode.
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	// Example endpoint for query parameter binding and validation.
	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"`
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			// BindAndValidate returns an HTTPError; GlobalErrorHandler will handle it.
			return err
		}
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	// --- API v1 Route Group ---
	apiV1Group := router.Group("/api/v1")
	// Apply authentication middleware to all routes within this group.
	// This will run after global middleware.
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	// Tasks API sub-group under /api/v1.
	tasksAPI := apiV1Group.Group("/tasks")
	{
		// Handler for POST /api/v1/tasks (Create a new task).
		postTaskHandler := func(c *xylium.Context) error {
			var req struct { // Anonymous struct for request binding.
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := c.BindAndValidate(&req); err != nil {
				return err // Validation errors handled by GlobalErrorHandler.
			}
			// Example of additional custom validation.
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
				// Return a structured error message using xylium.M.
				return xylium.NewHTTPError(xylium.StatusBadRequest,
					xylium.M{"field": "due_date", "error": "Due date must be today or in the future."})
			}
			now := time.Now().UTC()
			newTask := Task{
				ID: generateTaskID(), Title: req.Title, Description: req.Description,
				Completed: false, DueDate: req.DueDate, Tags: req.Tags,
				CreatedAt: now, UpdatedAt: now,
			}
			tasksDBLock.Lock()
			tasksDB[newTask.ID] = newTask
			tasksDBLock.Unlock()
			return c.JSON(http.StatusCreated, newTask)
		}

		// Configure a specific Rate Limiter for the task creation endpoint.
		// This uses a new, separate store to have behavior independent of the global limiter.
		// NOTE: The lifecycle (Close()) of this ad-hoc store needs careful management in a production app.
		// Here, it's not explicitly closed for simplicity, but it would leak a goroutine.
		createTaskLimiterStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(2 * time.Minute))
		// In a real app: defer a function that calls createTaskLimiterStore.Close() if main managed it,
		// or use a more structured resource management approach.

		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests:    5,                       // Allow only 5 task creations...
			WindowDuration: 1 * time.Minute,           // ...per minute per unique key.
			Store:          createTaskLimiterStore,
			Message:        "You are attempting to create tasks too frequently. Please wait a moment.",
			KeyGenerator: func(c *xylium.Context) string {
				// simpleAuthMiddleware has already run (as this is route middleware, and auth is on the group).
				apiKey := c.Header("X-API-Key")
				// Create a key based on IP and API key for more granular limiting.
				return "task_create_limit:" + c.RealIP() + ":" + apiKey
			},
			SendRateLimitHeaders: xylium.SendHeadersOnLimit, // Send X-RateLimit-* headers only when the client is limited.
		}
		// Apply this route-specific middleware. It runs after global and group middleware.
		tasksAPI.POST("", postTaskHandler, xylium.RateLimiter(createTaskRateLimiterConfig))

		// GET /api/v1/tasks - List all tasks.
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock() // Use read lock for concurrent-safe access.
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB)) // Pre-allocate slice capacity.
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			return c.JSON(http.StatusOK, taskList)
		})

		// GET /api/v1/tasks/:id - Get a specific task by ID.
		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id") // Get route parameter.
			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			return c.JSON(http.StatusOK, task)
		})

		// PUT /api/v1/tasks/:id - Update an existing task (supports partial updates).
		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")

			// To correctly handle partial updates (distinguish "field not sent" from "field sent as null"),
			// first, unmarshal to a map of raw JSON messages to check for key presence.
			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body() // Get raw request body once.
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Invalid JSON body for update (raw parse).").WithInternal(err)
			}

			tasksDBLock.Lock() // Obtain full write lock for the read-then-update.
			defer tasksDBLock.Unlock()

			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found for update.", taskID))
			}

			// Bind to a struct with pointers for optional fields.
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

			// Validate the request struct.
			currentValidator := xylium.GetValidator()
			if err := currentValidator.Struct(&req); err != nil {
				if vErrs, ok := err.(validator.ValidationErrors); ok { // Check for specific validation errors.
					errFields := make(map[string]string)
					for _, fe := range vErrs { // Format errors for a user-friendly response.
						// Provide more context in the error message.
						errMsg := fmt.Sprintf("Validation failed on '%s' tag for field '%s'", fe.Tag(), fe.Field())
						if fe.Value() != nil && fe.Value() != "" {
							errMsg += fmt.Sprintf(" (value: '%v')", fe.Value())
						}
						if fe.Param() != "" {
							errMsg += fmt.Sprintf(". Param: %s.", fe.Param())
						}
						errFields[fe.Namespace()] = errMsg // Use Namespace for potentially nested fields.
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed during update.", "details": errFields}).WithInternal(err)
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error during update.").WithInternal(err)
			}

			changed := false // Flag to track if any actual changes are made.
			if req.Title != nil {
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
			// Check if 'due_date' was explicitly present in the request using rawRequest.
			if _, dueDateInRequest := rawRequest["due_date"]; dueDateInRequest {
				if req.DueDate == nil { // Explicitly set to null.
					existingTask.DueDate = nil
				} else { // Value provided.
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(xylium.StatusBadRequest,
							xylium.M{"field": "due_date", "error": "Due date must be today or in the future if provided for update."})
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}
			// Check if 'tags' was explicitly present.
			if _, tagsInRequest := rawRequest["tags"]; tagsInRequest {
				if req.Tags == nil { // Explicitly set to null.
					existingTask.Tags = nil
				} else { // Value provided (could be empty array []).
					existingTask.Tags = *req.Tags
				}
				changed = true
			}

			if changed {
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask // Update in the "database".
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		// DELETE /api/v1/tasks/:id - Delete a task by ID.
		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock() // Obtain write lock.
			_, found := tasksDB[taskID]
			if found {
				delete(tasksDB, taskID) // Remove from map.
			}
			tasksDBLock.Unlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID))
			}
			return c.NoContent(http.StatusNoContent) // Standard HTTP 204 No Content for successful deletion.
		})

		// PATCH /api/v1/tasks/:id/complete - Mark a task as complete.
		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock() // Obtain write lock.
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found for completion.")
			}
			if task.Completed { // If already complete, operation is idempotent.
				return c.JSON(http.StatusOK, task) // Return current state.
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task // Save updated task.
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	// The router's logger will print the operating mode during startup
	// if ListenAndServeGracefully (or other start functions) are modified to do so.
	router.Logger().Printf("Task API (Complete Example) server starting. Listening on http://localhost%s", listenAddr)

	// Use ListenAndServeGracefully for safe shutdown, handling OS signals.
	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		appLogger.Fatalf("FATAL: API server encountered an error: %v", err)
	}

	// --- Cleanup Shared Resources on Shutdown ---
	// This code runs after ListenAndServeGracefully has completed (server has shut down).
	if closeSharedStoreFunc != nil {
		appLogger.Println("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appLogger.Printf("Error closing shared rate limiter store: %v", err)
		}
	}
	// Note: The ad-hoc `createTaskLimiterStore.Close()` is not managed here.
	// In a production application, all such stateful resources require proper cleanup mechanisms.

	appLogger.Println("Task API (Complete Example) server has shut down gracefully.")
}
