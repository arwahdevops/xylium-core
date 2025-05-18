// examples/complete.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // Import the Xylium framework
	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp"
)

// --- Data Model: Task (same as before) ---
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title" validate:"required,min=3,max=200"`
	Description string     `json:"description,omitempty"`
	Completed   bool       `json:"completed"`
	DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
	Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- In-Memory Data Storage (same as before) ---
var (
	tasksDB      = make(map[string]Task)
	tasksDBLock  sync.RWMutex
	nextTaskID   = 1
	taskIDPrefix = "task-"
)

func generateTaskID() string {
	id := fmt.Sprintf("%s%d", taskIDPrefix, nextTaskID)
	nextTaskID++
	return id
}

// --- Custom Middleware ---

// requestLoggerMiddleware logs request details using c.Logger().
func requestLoggerMiddleware() xylium.Middleware { // No xylium.Logger needed as param
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			// c.Logger() will include request_id if RequestID middleware is used.

			err := next(c) // Process request.

			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode()
			logger := c.Logger() // Get request-scoped logger.

			logFields := xylium.M{
				"method":     c.Method(),
				"path":       c.Path(),
				"status":     statusCode,
				"latency_ms": latency.Milliseconds(),
				"client_ip":  c.RealIP(),
				"user_agent": c.UserAgent(),
			}

			if statusCode >= 500 {
				logger.WithFields(logFields).Errorf("Request completed with server error.")
			} else if statusCode >= 400 {
				logger.WithFields(logFields).Warnf("Request completed with client error.")
			} else {
				logger.WithFields(logFields).Infof("Request completed.")
			}
			return err
		}
	}
}

// simpleAuthMiddleware provides API Key authentication using c.Logger().
func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			logger := c.Logger().WithFields(xylium.M{"middleware": "simpleAuth"})

			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				logger.Warnf("API key missing for %s %s.", c.Method(), c.Path())
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				logger.Warnf("Invalid API key provided for %s %s.", c.Method(), c.Path())
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API key provided.")
			}
			logger.Debugf("API key validated for %s %s.", c.Method(), c.Path())
			c.Set("authenticated_via", "APIKey")
			return next(c)
		}
	}
}

// --- Global Application Variables ---
var (
	startupTime            time.Time
	sharedRateLimiterStore xylium.LimiterStore // LimiterStore interface
	closeSharedStoreFunc   func() error
	// createTaskLimiterStore xylium.LimiterStore // If managing its lifecycle globally
	// closeCreateTaskStoreFunc func() error
)

// --- Main Application Entry Point ---
func main() {
	// --- Xylium Operating Mode (Optional) ---
	// Set via ENV: XYLIUM_MODE=debug or XYLIUM_MODE=release
	// Or programmatically: xylium.SetMode(xylium.DebugMode)

	startupTime = time.Now().UTC()

	// --- Xylium Router and Logger Initialization ---
	// Using router.New() for default logger behavior (auto-configured by Xylium mode).
	router := xylium.New()
	// Get the base application logger from the router and add global app fields.
	appBaseLogger := router.Logger().WithFields(xylium.M{"service": "TaskAPI-CompleteApp"})

	appBaseLogger.Infof("Application starting in Xylium '%s' mode.", router.CurrentMode())

	// --- Shared Rate Limiter Store Initialization ---
	var storeOpts []xylium.InMemoryStoreOption
	storeOpts = append(storeOpts, xylium.WithCleanupInterval(5*time.Minute))
	storeOpts = append(storeOpts, xylium.WithLogger(appBaseLogger.WithFields(xylium.M{"component": "SharedRateLimiterStore"})))

	concreteStore := xylium.NewInMemoryStore(storeOpts...)
	sharedRateLimiterStore = concreteStore
	closeSharedStoreFunc = concreteStore.Close

	// --- Xylium Server Configuration (Example of customizing with NewWithConfig) ---
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appBaseLogger // Use our customized appBaseLogger
	serverCfg.Name = "TaskManagementAPI-Complete/1.0.1"
	serverCfg.ReadTimeout = 32 * time.Second
	serverCfg.WriteTimeout = 32 * time.Second
	serverCfg.ShutdownTimeout = 22 * time.Second

	// Re-initialize router to apply this custom serverCfg
	router = xylium.NewWithConfig(serverCfg)


	// --- Global Middleware Registration ---
	router.Use(xylium.RequestID())        // 1. RequestID for c.Logger()
	router.Use(requestLoggerMiddleware()) // 2. Our request logger
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{ // 3. Timeout
		Timeout: 5 * time.Second, // Shorter timeout for this example
		Message: "The server is taking too long to respond.",
	}))
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{ // 4. CORS
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"},
		AllowMethods:     []string{xylium.MethodGet, xylium.MethodPost, xylium.MethodPut, xylium.MethodDelete, xylium.MethodOptions, xylium.MethodPatch},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader},
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true,
		MaxAge:           3600,
	}))
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{ // 5. Gzip
		Level:     fasthttp.CompressBestSpeed,
		MinLength: 1024,
	}))
	globalRateLimiterConfig := xylium.RateLimiterConfig{ // 6. Global Rate Limiter
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimiterStore,
		Skip:           func(c *xylium.Context) bool { return c.Path() == "/health" },
		Message: func(c *xylium.Context, limit int, window time.Duration, resetTime time.Time) string {
			ridVal, _ := c.Get(xylium.ContextKeyRequestID)
			rid, _ := ridVal.(string)
			return fmt.Sprintf(
				"[ReqID: %s] Too many requests from IP %s. Limit: %d per %v. Retry after: %s.",
				rid, c.RealIP(), limit, window, resetTime.Format(time.RFC1123),
			)
		},
		SendRateLimitHeaders: xylium.SendHeadersAlways,
		RetryAfterMode:       xylium.RetryAfterHTTPDate,
	}
	router.Use(xylium.RateLimiter(globalRateLimiterConfig))
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc { // 7. Security Headers
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			return next(c)
		}
	})

	// --- Application Route Definitions ---
	router.GET("/health", func(c *xylium.Context) error {
		logger := c.Logger().WithFields(xylium.M{"handler": "HealthCheck"})
		logger.Info("Health check endpoint accessed.")
		healthStatus := xylium.M{
			"status":      "healthy",
			"timestamp":   time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":      time.Since(startupTime).String(),
			"xylium_mode": c.RouterMode(),
		}
		logger.Debugf("Health status: %+v", healthStatus)
		return c.JSON(http.StatusOK, healthStatus)
	})

	type FilterRequest struct { // Same FilterRequest struct
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"`
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		logger := c.Logger().WithFields(xylium.M{"handler": "FilterTasks"})
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			logger.Warnf("Validation failed for /filter-tasks: %v", err)
			return err
		}
		logger.WithFields(xylium.M{"filters_applied": req}).Info("Filter parameters received and validated.")
		return c.JSON(http.StatusOK, xylium.M{"message": "Filter parameters received.", "filters": req})
	})

	apiV1Group := router.Group("/api/v1")
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	tasksAPI := apiV1Group.Group("/tasks")
	{
		postTaskHandler := func(c *xylium.Context) error {
			logger := c.Logger().WithFields(xylium.M{"handler": "CreateTask"})
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := c.BindAndValidate(&req); err != nil {
				logger.Warnf("Task creation validation failed: %v", err)
				return err
			}
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
				errMsg := "Due date must be today or in the future."
				logger.Warnf("Task creation: Invalid DueDate '%v'. %s", req.DueDate, errMsg)
				return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"field": "due_date", "error": errMsg})
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
			logger.WithFields(xylium.M{"task_id": newTask.ID, "task_title": newTask.Title}).Infof("Task created.")
			return c.JSON(http.StatusCreated, newTask)
		}

		var createTaskStoreOpts []xylium.InMemoryStoreOption
		createTaskStoreOpts = append(createTaskStoreOpts, xylium.WithCleanupInterval(2*time.Minute))
		createTaskStoreOpts = append(createTaskStoreOpts, xylium.WithLogger(appBaseLogger.WithFields(xylium.M{"component": "CreateTaskRateLimiterStore"})))
		createTaskLimiterStore := xylium.NewInMemoryStore(createTaskStoreOpts...)
		// defer createTaskLimiterStore.Close() // Manage lifecycle

		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests:    5,
			WindowDuration: 1 * time.Minute,
			Store:          createTaskLimiterStore,
			Message:        "You are attempting to create tasks too frequently. Please wait a moment.",
			KeyGenerator: func(c *xylium.Context) string {
				apiKey := c.Header("X-API-Key")
				return "task_create_limit:" + c.RealIP() + ":" + apiKey
			},
			SendRateLimitHeaders: xylium.SendHeadersOnLimit,
		}
		tasksAPI.POST("", postTaskHandler, xylium.RateLimiter(createTaskRateLimiterConfig))

		tasksAPI.GET("", func(c *xylium.Context) error {
			logger := c.Logger().WithFields(xylium.M{"handler": "ListTasks"})
			logger.Info("Listing all tasks.")
			tasksDBLock.RLock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			tasksDBLock.RUnlock()
			logger.Debugf("Found %d tasks.", len(taskList))
			return c.JSON(http.StatusOK, taskList)
		})

		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			logger := c.Logger().WithFields(xylium.M{"handler": "GetTaskByID", "task_id_param": taskID})
			logger.Debug("Attempting to retrieve task.")
			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()
			if !found {
				logger.Warnf("Task not found.")
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			logger.Debug("Task retrieved successfully.")
			return c.JSON(http.StatusOK, task)
		})

		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			logger := c.Logger().WithFields(xylium.M{"handler": "UpdateTask", "task_id_param": taskID})
			logger.Debug("Attempting to update task.")

			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body()
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				logger.Errorf("Invalid JSON body for update (raw parse): %v", err)
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Invalid JSON body for update.").WithInternal(err)
			}
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			existingTask, found := tasksDB[taskID]
			if !found {
				logger.Warnf("Task not found for update.")
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}

			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				logger.Errorf("Failed to parse JSON into update fields structure: %v", err)
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Failed to parse JSON for update.").WithInternal(err)
			}
			currentValidator := xylium.GetValidator()
			if err := currentValidator.Struct(&req); err != nil {
				logger.Warnf("Task update validation failed: %v", err)
				if vErrs, ok := err.(validator.ValidationErrors); ok {
					errFields := make(map[string]string)
					for _, fe := range vErrs {
						errMsg := fmt.Sprintf("Validation failed on '%s' tag for field '%s'", fe.Tag(), fe.Field())
						if fe.Value() != nil && fe.Value() != "" { errMsg += fmt.Sprintf(" (value: '%v')", fe.Value()) }
						if fe.Param() != "" { errMsg += fmt.Sprintf(". Param: %s.", fe.Param()) }
						errFields[fe.Namespace()] = errMsg
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed.", "details": errFields}).WithInternal(err)
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error.").WithInternal(err)
			}

			changed := false
			// ... (update logic same as before) ...
			if req.Title != nil { existingTask.Title = *req.Title; changed = true }
			if req.Description != nil { existingTask.Description = *req.Description; changed = true }
			if req.Completed != nil { existingTask.Completed = *req.Completed; changed = true }
			if _, duePresent := rawRequest["due_date"]; duePresent {
				if req.DueDate == nil { existingTask.DueDate = nil } else {
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"field": "due_date", "error": "Due date must be today or in the future."})
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}
			if _, tagsPresent := rawRequest["tags"]; tagsPresent {
				if req.Tags == nil { existingTask.Tags = nil } else { existingTask.Tags = *req.Tags }
				changed = true
			}

			if changed {
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask
				logger.WithFields(xylium.M{"fields_updated": changed}).Info("Task updated.")
			} else {
				logger.Info("Task update request received, but no changes applied.")
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			logger := c.Logger().WithFields(xylium.M{"handler": "DeleteTask", "task_id_param": taskID})
			logger.Info("Attempting to delete task.")
			tasksDBLock.Lock()
			_, found := tasksDB[taskID]
			if found { delete(tasksDB, taskID) }
			tasksDBLock.Unlock()
			if !found {
				logger.Warnf("Task not found for deletion.")
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			logger.Info("Task deleted successfully.")
			return c.NoContent(http.StatusNoContent)
		})

		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			logger := c.Logger().WithFields(xylium.M{"handler": "MarkTaskComplete", "task_id_param": taskID})
			logger.Info("Attempting to mark task as complete.")
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				logger.Warnf("Task not found for completion.")
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found.")
			}
			if task.Completed {
				logger.Debug("Task was already complete.")
				return c.JSON(http.StatusOK, task)
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task
			logger.Info("Task marked as complete.")
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	appBaseLogger.Infof("Task API (Complete Example) server starting. Listening on http://localhost%s", listenAddr)

	if err := router.Start(listenAddr); err != nil { // Use app.Start()
		appBaseLogger.Fatalf("FATAL: API server failed to start: %v", err)
	}

	// --- Cleanup Shared Resources on Shutdown ---
	if closeSharedStoreFunc != nil {
		appBaseLogger.Info("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appBaseLogger.Errorf("Error closing shared rate limiter store: %v", err)
		}
	}
	// Reminder: lifecycle of createTaskLimiterStore.Close() also needs management for production.

	appBaseLogger.Info("Task API (Complete Example) server has shut down gracefully.")
}
