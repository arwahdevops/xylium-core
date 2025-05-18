// examples/basic_auth_csrf.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path
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

// --- Custom Middleware Implementations ---

// requestLoggerMiddleware logs details of each incoming HTTP request using c.Logger().
func requestLoggerMiddleware() xylium.Middleware { // No longer needs xylium.Logger passed in
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			// Request ID will be part of c.Logger() if RequestID middleware is used prior.

			err := next(c) // Process the request.

			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode()
			logger := c.Logger() // Gets request-scoped logger (with request_id).

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

// simpleAuthMiddleware provides basic API key authentication using c.Logger().
func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			logger := c.Logger().WithFields(xylium.M{"middleware": "simpleAuth"}) // Add middleware context.

			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				logger.Warnf("API key missing in X-API-Key header for %s %s.", c.Method(), c.Path())
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				logger.Warnf("Invalid API key provided for %s %s.", c.Method(), c.Path())
				// Avoid logging the actual 'providedKey' in production for sensitive data.
				// Can log parts of it or a hash in debug if necessary.
				// logger.Debugf("Attempted API Key: %s (partial or hashed)", providedKey)
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API key provided.")
			}
			logger.Debugf("API key validated successfully for %s %s.", c.Method(), c.Path())
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
	// For specific rate limiter, if we want to manage its closure too
	// createTaskLimiterStore xylium.LimiterStore
	// closeCreateTaskStoreFunc func() error
)

// --- Main Application Entry Point ---
func main() {
	// --- Xylium Operating Mode Configuration (Optional) ---
	// Set via ENV: XYLIUM_MODE=debug or XYLIUM_MODE=release
	// Or programmatically (takes precedence if called before New/NewWithConfig):
	// xylium.SetMode(xylium.DebugMode)

	startupTime = time.Now().UTC()

	// --- Xylium Router and Logger Initialization ---
	// Xylium's New() or NewWithConfig(DefaultServerConfig()) will automatically
	// create and configure a DefaultLogger based on the Xylium operating mode.
	// If you need to customize the logger further (e.g., output to a file, different global fields),
	// you would create it explicitly and pass it via ServerConfig.
	/*
		// Example of explicit logger customization:
		explicitLogger := xylium.NewDefaultLogger()
		if xylium.Mode() == xylium.ReleaseMode {
			explicitLogger.SetFormatter(xylium.JSONFormatter)
			// explicitLogger.SetOutput(yourLogFile)
		}
		explicitLogger = explicitLogger.WithFields(xylium.M{"app": "TaskAPI-CSRF"}).(xylium.Logger) // Type assertion
		serverCfgForCustomLogger := xylium.DefaultServerConfig()
		serverCfgForCustomLogger.Logger = explicitLogger
		router := xylium.NewWithConfig(serverCfgForCustomLogger)
	*/

	// For this example, let's use the default behavior for simplicity.
	router := xylium.New()
	appBaseLogger := router.Logger().WithFields(xylium.M{"service": "TaskAPI-CSRFApp"}) // Get base logger and add service context

	appBaseLogger.Infof("Application starting in Xylium '%s' mode.", router.CurrentMode())

	// --- Shared Rate Limiter Store Initialization ---
	// Configure the InMemoryStore to use our application's base logger for its internal messages.
	var storeOpts []xylium.InMemoryStoreOption
	storeOpts = append(storeOpts, xylium.WithCleanupInterval(5*time.Minute))
	storeOpts = append(storeOpts, xylium.WithLogger(appBaseLogger.WithFields(xylium.M{"component": "SharedRateLimiterStore"})))

	concreteStore := xylium.NewInMemoryStore(storeOpts...)
	sharedRateLimiterStore = concreteStore
	closeSharedStoreFunc = concreteStore.Close

	// --- Xylium Server Configuration (adjust as needed) ---
	// DefaultServerConfig() is used by router.New(), so serverCfg here is effectively what router uses.
	// If we wanted to change server settings AFTER New(), it's not directly possible.
	// We'd need to pass a modified ServerConfig to NewWithConfig.
	// router.serverConfig.Name = "TaskManagementAPI-CSRF/1.0" // This would require serverConfig to be public or have setters.
	// For now, we rely on defaults or pass a full ServerConfig to NewWithConfig.
	// Let's assume we want to show how to modify server config with NewWithConfig:

	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appBaseLogger // Ensure our appBaseLogger is used
	serverCfg.Name = "TaskManagementAPI-CSRF/1.0.1"
	serverCfg.ReadTimeout = 35 * time.Second      // Example: slightly different timeout
	serverCfg.WriteTimeout = 35 * time.Second
	serverCfg.ShutdownTimeout = 25 * time.Second

	// Re-initialize router if we decided to use NewWithConfig with custom serverCfg
	// If we only used router.New(), then the logger config is done, but not server name/timeouts.
	// For clarity, let's re-initialize with the full config.
	router = xylium.NewWithConfig(serverCfg) // Now router uses serverCfg with our appBaseLogger & custom name/timeouts

	// --- Global Middleware Registration (Order Matters!) ---
	router.Use(xylium.RequestID())                 // 1. Adds X-Request-ID, makes it available for c.Logger()
	router.Use(requestLoggerMiddleware())          // 2. Our custom request logger
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{ // 3. Timeout
		Timeout: 10 * time.Second,
		Message: "The server is taking too long to respond.",
	}))
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{ // 4. CORS
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"},
		AllowMethods:     []string{xylium.MethodGet, xylium.MethodPost, xylium.MethodPut, xylium.MethodDelete, xylium.MethodOptions, xylium.MethodPatch},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader, "X-CSRF-Token"},
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true,
		MaxAge:           3600,
	}))
	router.Use(xylium.CSRFWithConfig(xylium.CSRFConfig{ // 5. CSRF
		CookieName:     "_csrf_app_token",
		CookieSecure:   false, // True in Production (HTTPS)
		CookieHTTPOnly: false, // JS needs to read for header
		CookieSameSite: fasthttp.CookieSameSiteLaxMode,
		HeaderName:     "X-CSRF-Token",
	}))
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{ // 6. Gzip
		Level:     fasthttp.CompressBestSpeed,
		MinLength: 1024, // Only compress responses > 1KB
	}))
	globalRateLimiterConfig := xylium.RateLimiterConfig{ // 7. Global Rate Limiter
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimiterStore, // Use our managed store
		Skip:           func(c *xylium.Context) bool { return c.Path() == "/health" },
		Message: func(c *xylium.Context, limit int, window time.Duration, resetTime time.Time) string {
			// Message uses c.Logger() if it needs to log anything internally, or just returns string.
			// For this example, the message itself can include info from c.Get(xylium.ContextKeyRequestID).
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
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc { // 8. Security Headers
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

		csrfTokenValue, _ := c.Get(xylium.DefaultCSRFConfig.ContextTokenKey) // Use configured key
		csrfTokenStr, _ := csrfTokenValue.(string)

		healthStatus := xylium.M{
			"status":          "healthy",
			"timestamp":       time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":          time.Since(startupTime).String(),
			"xylium_mode":     c.RouterMode(),
			"csrf_token_test": csrfTokenStr,
		}
		logger.Debugf("Health status: %+v", healthStatus)
		return c.JSON(http.StatusOK, healthStatus)
	})

	router.GET("/csrf-token", func(c *xylium.Context) error {
		logger := c.Logger().WithFields(xylium.M{"handler": "CSRFToken"})
		tokenVal, _ := c.Get(xylium.DefaultCSRFConfig.ContextTokenKey) // Use configured key
		tokenStr, _ := tokenVal.(string)
		logger.Infof("CSRF token requested and provided.")
		// logger.Debugf("CSRF token value: %s", tokenStr) // Avoid logging actual token unless necessary for deep debug
		return c.JSON(http.StatusOK, xylium.M{"csrf_token": tokenStr})
	})

	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"`
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		logger := c.Logger().WithFields(xylium.M{"handler": "FilterTasks"})
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			logger.Warnf("Validation failed for /filter-tasks: %v", err) // GlobalErrorHandler will log details of HTTPError
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
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				errMsg := "Due date must be today or in the future."
				logger.Warnf("Task creation: Invalid DueDate '%v'. %s", req.DueDate, errMsg)
				return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"due_date": errMsg})
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

		// Configure specific rate limiter store with logger
		var createTaskStoreOpts []xylium.InMemoryStoreOption
		createTaskStoreOpts = append(createTaskStoreOpts, xylium.WithCleanupInterval(2*time.Minute))
		createTaskStoreOpts = append(createTaskStoreOpts, xylium.WithLogger(appBaseLogger.WithFields(xylium.M{"component": "CreateTaskRateLimiterStore"})))
		createTaskLimiterStore := xylium.NewInMemoryStore(createTaskStoreOpts...)
		// defer createTaskLimiterStore.Close() // Manage lifecycle if needed, e.g., in a list of closers

		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests:    5,
			WindowDuration: 1 * time.Minute,
			Store:          createTaskLimiterStore,
			Message:        "You are attempting to create tasks too frequently. Please wait a moment.",
			KeyGenerator: func(c *xylium.Context) string {
				apiKey := c.Header("X-API-Key") // Assumes auth middleware ran
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
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
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
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
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
						errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v'). Param: %s.", fe.Tag(), fe.Value(), fe.Param())
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed.", "details": errFields}).WithInternal(err)
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error.").WithInternal(err)
			}

			changed := false
			if req.Title != nil { existingTask.Title = *req.Title; changed = true }
			// ... (rest of update logic) ...
			if req.Description != nil { existingTask.Description = *req.Description; changed = true }
			if req.Completed != nil { existingTask.Completed = *req.Completed; changed = true }
			if _, duePresent := rawRequest["due_date"]; duePresent {
				if req.DueDate == nil { existingTask.DueDate = nil } else {
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"due_date": "Due date must be today or in the future."})
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
				return xylium.NewHTTPError(xylium.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
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
				return xylium.NewHTTPError(xylium.StatusNotFound, "Task not found.")
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

	adminGroup := router.Group("/admin")
	basicAuthValidator := func(username, password string, c *xylium.Context) (user interface{}, valid bool, err error) {
		logger := c.Logger().WithFields(xylium.M{"validator": "BasicAuth"}) // Use context logger
		if username == "admin" && password == "s3cr3tP@sswOrd" {
			logger.Debugf("BasicAuth successful for user '%s'", username)
			return xylium.M{"username": username, "role": "administrator"}, true, nil
		}
		logger.Warnf("BasicAuth failed for user '%s'", username)
		return nil, false, nil
	}
	adminGroup.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{Validator: basicAuthValidator, Realm: "Secure Admin Area"}))
	{
		adminGroup.GET("/dashboard", func(c *xylium.Context) error {
			userVal, _ := c.Get("user")
			c.Logger().WithFields(xylium.M{"handler": "AdminDashboard", "user_info": userVal}).Info("Admin dashboard accessed.")
			return c.JSON(http.StatusOK, xylium.M{"message": "Welcome Admin!", "user_info": userVal, "mode": c.RouterMode()})
		})
		adminGroup.POST("/settings", func(c *xylium.Context) error {
			c.Logger().WithFields(xylium.M{"handler": "AdminSettingsUpdate"}).Info("Admin settings update attempted (CSRF check applies).")
			return c.JSON(http.StatusOK, xylium.M{"message": "Admin settings updated (mock)."})
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	appBaseLogger.Infof("Xylium server (TaskAPI with CSRF & BasicAuth) starting. Listening on http://localhost%s", listenAddr)

	if err := router.Start(listenAddr); err != nil { // Use app.Start()
		appBaseLogger.Fatalf("FATAL: API server encountered an error during startup: %v", err)
	}

	// --- Cleanup Shared Resources on Shutdown ---
	if closeSharedStoreFunc != nil {
		appBaseLogger.Info("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appBaseLogger.Errorf("Error closing shared rate limiter store: %v", err)
		}
	}
	// Note: Lifecycle of createTaskLimiterStore.Close() is not explicitly managed here for brevity in example.
	// In a real app, all closable resources should be managed.

	appBaseLogger.Info("Task API server has shut down gracefully.")
}
