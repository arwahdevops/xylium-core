// examples/main.go
package main

import (
	"encoding/json" // For handling JSON in PUT requests (if still needed, was for rawRequest)
	"fmt"
	"net/http" // For http.StatusOK, http.StatusCreated, etc.
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium" // Adjust import path if necessary
	"github.com/go-playground/validator/v10"       // For validation error type assertion
)

// --- Task Data Model (same as before) ---
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

// --- Simple In-memory Task Storage (same as before) ---
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

// --- Custom Middleware using the new logger ---

// requestLoggerMiddleware logs details of each incoming request using c.Logger().
func requestLoggerMiddleware() xylium.Middleware { // No longer needs logger passed in
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			// Request ID is automatically included if RequestID middleware is used before this
			// and c.Logger() is called.

			// Process the request.
			err := next(c)

			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode()

			// Use c.Logger() to get a logger that includes request_id.
			// Log at INFO level for successful requests, WARN/ERROR for client/server errors.
			logger := c.Logger()
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

// simpleAuthMiddleware provides basic API key authentication.
func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			// Get logger from context, includes request_id.
			// Add middleware-specific field.
			logger := c.Logger().WithFields(xylium.M{"middleware": "simpleAuth"})

			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				logger.Warnf("API key missing in X-API-Key header for %s %s.", c.Method(), c.Path())
				return xylium.NewHTTPError(xylium.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				// Be cautious about logging the providedKey if it's sensitive.
				// For this example, we log it at a debug level if needed, or just warn about invalid attempt.
				logger.Warnf("Invalid API key provided (attempted) for %s %s.", c.Method(), c.Path())
				// logger.Debugf("Invalid API key detail: provided='%s', expected_suffix='...%s'", providedKey, validAPIKey[len(validAPIKey)-3:])
				return xylium.NewHTTPError(xylium.StatusForbidden, "Invalid API key provided.")
			}
			logger.Debugf("API key validated successfully for %s %s.", c.Method(), c.Path())
			c.Set("authenticated_via", "APIKey")
			return next(c)
		}
	}
}

var startupTime time.Time

func main() {
	// --- Xylium Mode Configuration ---
	// Set via environment variable XYLIUM_MODE (e.g., "debug", "release")
	// OR programmatically (highest precedence if called):
	// xylium.SetMode(xylium.DebugMode)
	// xylium.SetMode(xylium.ReleaseMode)

	startupTime = time.Now().UTC()

	// --- Xylium Router Initialization ---
	// If you want to customize the logger globally for the app (e.g., different output, JSON format for ReleaseMode):
	// myAppLogger := xylium.NewDefaultLogger()
	// if xylium.Mode() == xylium.ReleaseMode {
	// 	myAppLogger.SetFormatter(xylium.JSONFormatter)
	// 	myAppLogger.SetLevel(xylium.LevelInfo)
	// 	myAppLogger.EnableCaller(false)
	// } else {
	// 	myAppLogger.SetLevel(xylium.LevelDebug)
	// 	myAppLogger.EnableCaller(true)
	// 	myAppLogger.EnableColor(true) // DefaultLogger checks TTY internally
	// }
	// myAppLogger = myAppLogger.WithFields(xylium.M{"app_name": "TaskAPI", "app_version": "1.0.0"}).(xylium.Logger)

	// serverCfg := xylium.DefaultServerConfig()
	// serverCfg.Logger = myAppLogger // Pass your customized logger
	// serverCfg.Name = "TaskManagementAPI/1.0"
	// serverCfg.ReadTimeout = 30 * time.Second
	// router := xylium.NewWithConfig(serverCfg)

	// For simplicity in this example, we'll use the default logger behavior,
	// which Xylium configures automatically based on the mode.
	router := xylium.New() // Logger will be auto-configured by Xylium based on mode.

	// Get the application's base logger from the router.
	// This logger already reflects mode-based configurations.
	appLogger := router.Logger().WithFields(xylium.M{"service_context": "application_setup"})

	appLogger.Infof("Application starting in Xylium '%s' mode.", router.CurrentMode())
	if router.CurrentMode() == xylium.DebugMode {
		appLogger.Debug("Debug mode is active, verbose logging enabled.")
	}

	// --- Global Middleware Registration ---
	router.Use(xylium.RequestID())        // Adds X-Request-ID header and sets ContextKeyRequestID for c.Logger()
	router.Use(requestLoggerMiddleware()) // Our custom request logger using c.Logger()

	// Custom Security Headers Middleware.
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			if c.RouterMode() == xylium.DebugMode {
				c.SetHeader("X-Xylium-Debug", "true")
			}
			return next(c)
		}
	})

	// --- Route Definitions ---
	router.GET("/health", func(c *xylium.Context) error {
		// c.Logger() here will include 'request_id'.
		c.Logger().Infof("Health check accessed from IP: %s", c.RealIP())
		healthStatus := xylium.M{
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":    time.Since(startupTime).String(),
			"mode":      c.RouterMode(),
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	router.GET("/force-error", func(c *xylium.Context) error {
		c.Logger().Warn("Forcing a generic error for demonstration.")
		return fmt.Errorf("a deliberate generic error occurred")
	})

	router.GET("/force-httperror", func(c *xylium.Context) error {
		c.Logger().Warn("Forcing an HTTPError for demonstration.")
		internalCause := fmt.Errorf("simulated database connection failure")
		return xylium.NewHTTPError(http.StatusServiceUnavailable, "Service is temporarily down.").WithInternal(internalCause)
	})

	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty,ltfield=EndDate"`
		EndDate    *time.Time `query:"endDate" validate:"omitempty"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			// BindAndValidate returns an HTTPError; GlobalErrorHandler will log it.
			c.Logger().Debugf("Validation failed for /filter-tasks: %v (raw error from BindAndValidate)", err)
			return err
		}
		c.Logger().WithFields(xylium.M{"filters_applied": req}).Infof("Filter parameters received for /filter-tasks.")
		return c.JSON(http.StatusOK, xylium.M{
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	apiV1Group := router.Group("/api/v1")
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	tasksAPI := apiV1Group.Group("/tasks")
	{
		tasksAPI.GET("", func(c *xylium.Context) error {
			// Add handler-specific logging context
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "ListTasks"})
			handlerLogger.Info("Listing all tasks.")

			tasksDBLock.RLock()
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			handlerLogger.Debugf("Found %d tasks to list.", len(taskList))
			return c.JSON(http.StatusOK, taskList)
		})

		tasksAPI.POST("", func(c *xylium.Context) error {
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "CreateTask"})
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}

			if err := c.BindAndValidate(&req); err != nil {
				// GlobalErrorHandler will log the HTTPError details.
				// We can add a specific debug log here if needed.
				handlerLogger.Debugf("Task creation validation failed: %v", err)
				return err
			}

			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				msg := "Due date must be today or in the future."
				handlerLogger.Warnf("Task creation failed due to invalid due_date: %s", req.DueDate.String())
				return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"due_date": msg})
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

			handlerLogger.WithFields(xylium.M{"task_id": newTask.ID, "task_title": newTask.Title}).Infof("Task created successfully.")
			return c.JSON(http.StatusCreated, newTask)
		})

		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "GetTaskByID", "task_id_param": taskID})
			handlerLogger.Debug("Attempting to retrieve task.")

			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()

			if !found {
				handlerLogger.Warnf("Task with ID '%s' not found.", taskID)
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			handlerLogger.Debug("Task found successfully.")
			return c.JSON(http.StatusOK, task)
		})

		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "UpdateTask", "task_id_param": taskID})
			handlerLogger.Debug("Attempting to update task.")

			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			existingTask, found := tasksDB[taskID]
			if !found {
				handlerLogger.Warnf("Task with ID '%s' not found for update.", taskID)
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for update.", taskID))
			}

			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body()
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				handlerLogger.Errorf("Invalid JSON body for update (raw parse): %v", err)
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Invalid JSON body for update.").WithInternal(err)
			}

			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				handlerLogger.Errorf("Failed to parse JSON for update fields: %v", err)
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Failed to parse JSON for update fields.").WithInternal(err)
			}
			currentValidator := xylium.GetValidator()
			if err := currentValidator.Struct(&req); err != nil {
				handlerLogger.Debugf("Task update validation failed: %v", err)
				// Let BindAndValidate style error formatting occur (GlobalErrorHandler handles it)
				// This requires converting validator.ValidationErrors to our HTTPError with details.
				// For simplicity, we'll just pass a generic validation message if not using BindAndValidate for PUT.
				if vErrs, ok := err.(validator.ValidationErrors); ok {
					errFields := make(map[string]string)
					for _, fe := range vErrs {
						errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v'). Param: %s.", fe.Tag(), fe.Value(), fe.Param())
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed during update.", "details": errFields}).WithInternal(err)
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error during update.").WithInternal(err)
			}

			changed := false
			// ... (logic for updating fields, same as before) ...
			if req.Title != nil { existingTask.Title = *req.Title; changed = true }
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
				handlerLogger.WithFields(xylium.M{"fields_changed": changed}).Info("Task updated successfully.")
			} else {
				handlerLogger.Info("Task update requested, but no changes detected.")
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "DeleteTask", "task_id_param": taskID})
			handlerLogger.Info("Attempting to delete task.")

			tasksDBLock.Lock()
			_, found := tasksDB[taskID]
			if found {
				delete(tasksDB, taskID)
			}
			tasksDBLock.Unlock()

			if !found {
				handlerLogger.Warnf("Task with ID '%s' not found for deletion.", taskID)
				return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID))
			}
			handlerLogger.Info("Task deleted successfully.")
			return c.NoContent(http.StatusNoContent)
		})

		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			handlerLogger := c.Logger().WithFields(xylium.M{"handler": "MarkTaskComplete", "task_id_param": taskID})
			handlerLogger.Info("Attempting to mark task as complete.")

			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				handlerLogger.Warnf("Task with ID '%s' not found for completion.", taskID)
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found for completion.")
			}
			if task.Completed {
				handlerLogger.Debug("Task was already complete.")
				return c.JSON(http.StatusOK, task)
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task
			handlerLogger.Info("Task marked as complete.")
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Start Xylium Server ---
	listenAddr := ":8080"
	// Use the application's base logger for this startup message.
	appLogger.Infof("Xylium server starting. Listening on http://localhost%s", listenAddr)

	// Use router.Start() for graceful shutdown.
	if err := router.Start(listenAddr); err != nil {
		// If server fails to start, log as Fatal.
		appLogger.Fatalf("FATAL: API server failed to start: %v", err)
	}

	// This part will only be reached after the server has shut down.
	appLogger.Info("Task API server has shut down gracefully.")
}
