// examples/main.go
package main

import (
	"encoding/json" // For handling JSON in PUT requests
	"fmt"
	"log"
	"net/http" // For http.StatusOK, http.StatusCreated, etc.
	"os"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust if your import path changes based on your project structure
	"github.com/go-playground/validator/v10"       // For validation error type assertion
)

// --- Task Data Model ---
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title" validate:"required,min=3,max=200"`
	Description string     `json:"description,omitempty"`
	Completed   bool       `json:"completed"`
	DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"` // gt ensures DueDate is in the future if provided
	Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- Simple In-memory Task Storage ---
// For demonstration purposes. In a real application, use a persistent database.
var (
	tasksDB      = make(map[string]Task) // In-memory map to store tasks.
	tasksDBLock  sync.RWMutex            // Mutex to protect concurrent access to tasksDB.
	nextTaskID   = 1                     // Simple counter for generating task IDs.
	taskIDPrefix = "task-"               // Prefix for task IDs.
)

// generateTaskID creates a unique ID for new tasks.
func generateTaskID() string {
	id := fmt.Sprintf("%s%d", taskIDPrefix, nextTaskID)
	nextTaskID++
	return id
}

// --- Custom Middleware ---

// requestLoggerMiddleware logs details of each incoming request.
// It's good practice to place it early in the middleware chain, ideally after RequestID middleware.
func requestLoggerMiddleware(logger xylium.Logger) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			// Attempt to get RequestID from context if RequestID middleware is used.
			reqIDVal, _ := c.Get(xylium.ContextKeyRequestID) // Using the constant from xylium package.
			reqIDStr, _ := reqIDVal.(string)

			// Process the request by calling the next handler in the chain.
			err := next(c)

			// After the handler has processed, log details.
			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode() // Get status code from fasthttp context.

			logMessage := fmt.Sprintf("ClientIP: %s | Method: %s | Path: %s | Status: %d | Latency: %s | UserAgent: \"%s\"",
				c.RealIP(),    // Get the real client IP.
				c.Method(),    // Get HTTP method.
				c.Path(),      // Get request path.
				statusCode,    // Get response status code.
				latency,       // Get request processing latency.
				c.UserAgent(), // Get client's User-Agent.
			)

			if reqIDStr != "" {
				logger.Printf("[ReqID: %s] %s", reqIDStr, logMessage)
			} else {
				logger.Printf("%s", logMessage)
			}
			return err // Return any error that occurred in the handler chain.
		}
	}
}

// simpleAuthMiddleware provides basic API key authentication.
// It checks for an "X-API-Key" header.
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
			c.Set("authenticated_via", "APIKey") // Store authentication info in the context for downstream handlers.
			return next(c)                       // Proceed to the next handler if authentication is successful.
		}
	}
}

var startupTime time.Time // Global variable to track application startup time for uptime calculation.

func main() {
	// --- Xylium Mode Configuration ---
	// You can set the mode programmatically before initializing the router.
	// If not set here, Xylium defaults to "release" mode or respects the
	// value of the XYLIUM_MODE environment variable if it's set.
	// Examples:
	// xylium.SetMode(xylium.DebugMode)
	// xylium.SetMode(xylium.TestMode)

	startupTime = time.Now().UTC()

	// --- Application Logger Setup ---
	appLogger := log.New(os.Stdout, "[TaskAPIApp] ", log.LstdFlags|log.Lshortfile)

	// --- Xylium Server Configuration ---
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger // Use our custom application logger for the Xylium server.
	serverCfg.Name = "TaskManagementAPI/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second // Timeout for graceful shutdown.

	// --- Xylium Router Initialization ---
	// The router will pick up the operating mode (from SetMode or ENV var).
	router := xylium.NewWithConfig(serverCfg)

	// Log the current operating mode of the router.
	router.Logger().Printf("Application starting in Xylium '%s' mode.", router.CurrentMode())

	// --- Global Middleware Registration ---
	// Middleware is executed in the order it is added.

	// 1. RequestID Middleware: Adds a unique ID to each request for tracing.
	router.Use(xylium.RequestID()) // Uses default "X-Request-ID" header.

	// 2. Request Logger Middleware: Logs details for every request.
	router.Use(requestLoggerMiddleware(router.Logger())) // Pass the router's logger.

	// 3. Custom Security Headers Middleware.
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			// Example: Add a custom header conditionally based on the operating mode.
			if c.RouterMode() == xylium.DebugMode { // CORRECTED: Use c.RouterMode()
				c.SetHeader("X-Debug-Mode-Active", "true")
			}
			return next(c)
		}
	})

	// --- Route Definitions ---

	// Health check endpoint.
	router.GET("/health", func(c *xylium.Context) error {
		healthStatus := xylium.M{ // Using xylium.M (map[string]interface{}) for convenience.
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":    time.Since(startupTime).String(),
			"mode":      c.RouterMode(), // CORRECTED: Show current Xylium mode in health status.
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	// Endpoint to demonstrate a forced generic error for testing GlobalErrorHandler.
	router.GET("/force-error", func(c *xylium.Context) error {
		// This error will be caught by Xylium's GlobalErrorHandler.
		// In DebugMode, the response might include more details.
		return fmt.Errorf("a deliberate generic error occurred for demonstration")
	})

	// Endpoint to demonstrate a forced HTTPError.
	router.GET("/force-httperror", func(c *xylium.Context) error {
		// This HTTPError will also be handled by GlobalErrorHandler.
		// The 'Internal' error part might be shown in the response if in DebugMode.
		internalCause := fmt.Errorf("simulated database connection failure")
		return xylium.NewHTTPError(http.StatusServiceUnavailable, "Service is temporarily down, please try again later.").WithInternal(internalCause)
	})

	// Endpoint to demonstrate query parameter binding and validation.
	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"` // StartDate must be less than EndDate if both provided.
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"` // dive validates each element in slice.
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			// BindAndValidate returns an HTTPError, which GlobalErrorHandler will process.
			// The response will detail validation failures.
			return err
		}
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	// --- API v1 Route Group ---
	apiV1Group := router.Group("/api/v1")
	// Apply authentication middleware to all routes within the /api/v1 group.
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	// Tasks API sub-group under /api/v1
	tasksAPI := apiV1Group.Group("/tasks")
	{
		// GET /api/v1/tasks - List all tasks.
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock() // Read lock for safe concurrent access.
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			return c.JSON(http.StatusOK, taskList)
		})

		// POST /api/v1/tasks - Create a new task.
		tasksAPI.POST("", func(c *xylium.Context) error {
			// Using an anonymous struct for request binding and validation.
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"` // gt = greater than current time.
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}

			if err := c.BindAndValidate(&req); err != nil {
				return err // GlobalErrorHandler handles formatting this error for the client.
			}

			// Additional custom validation example.
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				return xylium.NewHTTPError(xylium.StatusBadRequest,
					xylium.M{"due_date": "Due date must be today or in the future."})
			}

			now := time.Now().UTC()
			newTask := Task{
				ID:          generateTaskID(),
				Title:       req.Title,
				Description: req.Description,
				Completed:   false,
				DueDate:     req.DueDate,
				Tags:        req.Tags,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			tasksDBLock.Lock() // Write lock for modifying the tasksDB.
			tasksDB[newTask.ID] = newTask
			tasksDBLock.Unlock()
			return c.JSON(http.StatusCreated, newTask) // Respond with the created task.
		})

		// GET /api/v1/tasks/:id - Get a specific task by its ID.
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

		// PUT /api/v1/tasks/:id - Update an existing task (allows partial updates).
		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")

			tasksDBLock.Lock() // Obtain a full lock for read-then-write operation.
			defer tasksDBLock.Unlock()

			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found for update.", taskID))
			}

			// To handle partial updates correctly (distinguish between a field not sent vs. sent with a null/empty value),
			// first unmarshal to a map to check for key presence.
			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body() // Get the raw request body.
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Invalid JSON body for update (raw parse).").WithInternal(err)
			}

			// Then, bind to a struct with pointers for optional fields to capture provided values.
			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Failed to parse JSON for update fields.").WithInternal(err)
			}

			// Validate the request struct (only fields present and non-nil will be validated effectively due to omitempty and pointers).
            currentValidator := xylium.GetValidator() // Get Xylium's validator instance.
            if err := currentValidator.Struct(&req); err != nil {
                if vErrs, ok := err.(validator.ValidationErrors); ok { // Check if it's validation errors.
                    errFields := make(map[string]string)
                    for _, fe := range vErrs { // Format validation errors nicely.
                        errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v'). Param: %s.", fe.Tag(), fe.Value(), fe.Param())
                    }
                    return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed during update.", "details": errFields}).WithInternal(err)
                }
                // If not validator.ValidationErrors, it's some other processing error with the validator.
                return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error during update.").WithInternal(err)
            }

			changed := false // Flag to track if any field was actually changed.
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

			// Check if 'due_date' was explicitly present in the request payload using the rawRequest map.
			if _, duePresent := rawRequest["due_date"]; duePresent {
				if req.DueDate == nil { // Field 'due_date' was present and set to null.
					existingTask.DueDate = nil
				} else { // Field 'due_date' was present with a value.
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(xylium.StatusBadRequest,
							xylium.M{"due_date": "Due date must be today or in the future if provided for update."})
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}

			// Check if 'tags' was explicitly present.
			if _, tagsPresent := rawRequest["tags"]; tagsPresent {
				if req.Tags == nil { // Field 'tags' was present and set to null.
					existingTask.Tags = nil
				} else { // Field 'tags' was present with a value (could be an empty array []).
					existingTask.Tags = *req.Tags
				}
				changed = true
			}

			if changed {
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask // Update the task in our "database".
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		// DELETE /api/v1/tasks/:id - Delete a task by its ID.
		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock()
			_, found := tasksDB[taskID]
			if found {
				delete(tasksDB, taskID) // Remove from map.
			}
			tasksDBLock.Unlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID))
			}
			return c.NoContent(http.StatusNoContent) // Standard response for successful DELETE.
		})

		// PATCH /api/v1/tasks/:id/complete - Mark a task as complete.
		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found for completion.")
			}
			if task.Completed { // If already complete, the operation is idempotent.
				return c.JSON(http.StatusOK, task)
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	// The logger used here (router.Logger()) will print the server's operating mode
	// during startup if the server start functions (e.g., ListenAndServeGracefully)
	// have been modified to include it (as shown in previous steps).
	// Example log: "Xylium server listening gracefully on :8080 (Mode: debug)"

	// Use ListenAndServeGracefully for safe shutdown handling OS signals.
	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		// Use the initial appLogger here, as router.Logger() might not be fully available
		// if the server initialization failed very early.
		appLogger.Fatalf("FATAL: API server encountered an error: %v", err)
	}

	appLogger.Println("Task API server has shut down gracefully.")
}
