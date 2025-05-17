package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
	"github.com/go-playground/validator/v10"
	"github.com/valyala/fasthttp" // Diperlukan untuk fasthttp.CookieSameSiteLaxMode
)

// --- Model Data ---
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

// --- Penyimpanan Data (In-Memory) ---
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

// --- Middleware Kustom ---
func requestLoggerMiddleware(logger xylium.Logger) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			err := next(c)
			latency := time.Since(startTime)
			statusCode := c.Ctx.Response.StatusCode()
			requestIDVal, _ := c.Get(xylium.ContextKeyRequestID)
			requestIDStr, _ := requestIDVal.(string)

			logMessage := fmt.Sprintf("ClientIP: %s | Method: %s | Path: %s | UserAgent: \"%s\" | Status: %d | Latency: %s",
				c.RealIP(), c.Method(), c.Path(), c.UserAgent(), statusCode, latency)
			if requestIDStr != "" {
				logger.Printf("[ReqID: %s] %s", requestIDStr, logMessage)
			} else {
				logger.Printf("%s", logMessage)
			}
			return err
		}
	}
}

func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				return xylium.NewHTTPError(http.StatusUnauthorized, "API key is required in X-API-Key header.")
			}
			if providedKey != validAPIKey {
				return xylium.NewHTTPError(http.StatusForbidden, "Invalid API key provided.")
			}
			c.Set("authenticated_via", "APIKey")
			return next(c)
		}
	}
}

// --- Variabel Global Aplikasi ---
var startupTime time.Time
var sharedRateLimiterStore xylium.LimiterStore
var closeSharedStoreFunc func() error

// --- Fungsi Utama (Entry Point Aplikasi) ---
func main() {
	startupTime = time.Now().UTC()
	appLogger := log.New(os.Stdout, "[TaskAPIApp] ", log.LstdFlags|log.Lshortfile)

	{
		concreteStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(5 * time.Minute))
		sharedRateLimiterStore = concreteStore
		closeSharedStoreFunc = concreteStore.Close
	}

	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger
	serverCfg.Name = "TaskManagementAPI/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second

	router := xylium.NewWithConfig(serverCfg)

	// --- Pendaftaran Middleware Global (Urutan Eksekusi Penting!) ---
	router.Use(xylium.RequestID())
	router.Use(requestLoggerMiddleware(appLogger))
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
		Timeout: 5 * time.Second,
		Message: "The server is taking too long to respond.",
	}))
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader, "X-CSRF-Token"},
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true,
		MaxAge:           3600,
	}))
	router.Use(xylium.CSRFWithConfig(xylium.CSRFConfig{
		CookieSecure:   false,
		CookieHTTPOnly: false,
		// PERBAIKAN: Gunakan konstanta langsung dari paket fasthttp
		CookieSameSite: fasthttp.CookieSameSiteLaxMode,
	}))
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{
		Level:     fasthttp.CompressBestSpeed,
		MinLength: 1024,
	}))
	globalRateLimiterConfig := xylium.RateLimiterConfig{
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimiterStore,
		Skip: func(c *xylium.Context) bool { return c.Path() == "/health" },
		Message: func(c *xylium.Context, limit int, window time.Duration, resetTime time.Time) string {
			rid, _ := c.Get(xylium.ContextKeyRequestID)
			return fmt.Sprintf(
				"[ReqID: %v] Too many requests from IP %s. Limit: %d per %v. Retry after: %s.",
				rid, c.RealIP(), limit, window, resetTime.Format(time.RFC1123),
			)
		},
		SendRateLimitHeaders: xylium.SendHeadersAlways,
		RetryAfterMode:       xylium.RetryAfterHTTPDate,
	}
	router.Use(xylium.RateLimiter(globalRateLimiterConfig))
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			return next(c)
		}
	})

	// --- Pendaftaran Rute Aplikasi ---
	router.GET("/health", func(c *xylium.Context) error {
		healthStatus := xylium.M{ // PERBAIKAN: Menggunakan xylium.M
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":    time.Since(startupTime).String(),
		}
		return c.JSON(http.StatusOK, healthStatus)
	})
	router.GET("/csrf-token", func(c *xylium.Context) error {
		tokenVal, _ := c.Get("csrf_token")
		tokenStr, _ := tokenVal.(string)
		return c.JSON(http.StatusOK, xylium.M{"csrf_token": tokenStr}) // PERBAIKAN: Menggunakan xylium.M
	})

	type FilterRequest struct {
		StartDate  *time.Time `query:"startDate" validate:"omitempty"`
		EndDate    *time.Time `query:"endDate" validate:"omitempty,gtfield=StartDate"`
		Status     []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int      `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, xylium.M{ // PERBAIKAN: Menggunakan xylium.M
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	apiV1Group := router.Group("/api/v1")
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	tasksAPI := apiV1Group.Group("/tasks")
	{
		postTaskHandler := func(c *xylium.Context) error {
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := c.BindAndValidate(&req); err != nil { return err }
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				return xylium.NewHTTPError(http.StatusBadRequest,
					xylium.M{"due_date": "Due date must be today or in the future."}) // PERBAIKAN: xylium.M
			}
			now := time.Now().UTC()
			newTask := Task{
				ID: generateTaskID(), Title: req.Title, Description: req.Description,
				Completed: false, DueDate: req.DueDate, Tags: req.Tags,
				CreatedAt: now, UpdatedAt: now,
			}
			tasksDBLock.Lock(); tasksDB[newTask.ID] = newTask; tasksDBLock.Unlock()
			return c.JSON(http.StatusCreated, newTask)
		}
		createTaskLimiterStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(2 * time.Minute))
		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests: 5, WindowDuration: 1 * time.Minute, Store: createTaskLimiterStore,
			Message: "Too many task creation attempts. Please wait.",
			KeyGenerator: func(c *xylium.Context) string {
				apiKey := c.Header("X-API-Key")
				return "task_create_api:" + c.RealIP() + ":" + apiKey
			},
			SendRateLimitHeaders: xylium.SendHeadersOnLimit,
		}
		tasksAPI.POST("", postTaskHandler, xylium.RateLimiter(createTaskRateLimiterConfig))
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock(); defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB { taskList = append(taskList, task) }
			return c.JSON(http.StatusOK, taskList)
		})
		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id"); tasksDBLock.RLock(); task, found := tasksDB[taskID]; tasksDBLock.RUnlock()
			if !found { return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found.", taskID)) }
			return c.JSON(http.StatusOK, task)
		})
		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body()
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Invalid JSON body for update.").WithInternal(err)
			}
			_, dueDateInRequest := rawRequest["due_date"]; _, tagsInRequest := rawRequest["tags"]
			tasksDBLock.Lock(); defer tasksDBLock.Unlock()
			existingTask, found := tasksDB[taskID]
			if !found { return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for update.", taskID)) }
			var req struct {
				Title *string `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string `json:"description,omitempty"`
				Completed *bool `json:"completed,omitempty"`
				DueDate *time.Time `json:"due_date,omitempty" validate:"omitempty"`
				Tags *[]string `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Failed to parse JSON for update.").WithInternal(err)
			}
			currentValidator := xylium.GetValidator()
			if err := currentValidator.Struct(&req); err != nil {
				if vErrs, ok := err.(validator.ValidationErrors); ok {
					errFields := make(map[string]string)
					for _, fe := range vErrs {
						errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v'). Param: %s.", fe.Tag(), fe.Value(), fe.Param())
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, xylium.M{"message": "Validation failed during update.", "details": errFields}).WithInternal(err) // PERBAIKAN: xylium.M
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error.").WithInternal(err)
			}
			changed := false
			if req.Title != nil { existingTask.Title = *req.Title; changed = true }
			if req.Description != nil { existingTask.Description = *req.Description; changed = true }
			if req.Completed != nil { existingTask.Completed = *req.Completed; changed = true }
			if dueDateInRequest {
				if req.DueDate == nil { existingTask.DueDate = nil
				} else {
					if req.DueDate.Before(time.Now().UTC().Truncate(24 * time.Hour)) {
						return xylium.NewHTTPError(http.StatusBadRequest, xylium.M{"due_date": "Due date must be today or in the future."}) // PERBAIKAN: xylium.M
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}
			if tagsInRequest {
				if req.Tags == nil { existingTask.Tags = nil
				} else { existingTask.Tags = *req.Tags }
				changed = true
			}
			if changed { existingTask.UpdatedAt = time.Now().UTC(); tasksDB[taskID] = existingTask }
			return c.JSON(http.StatusOK, existingTask)
		})
		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id"); tasksDBLock.Lock(); _, found := tasksDB[taskID]
			if found { delete(tasksDB, taskID) }
			tasksDBLock.Unlock()
			if !found { return xylium.NewHTTPError(http.StatusNotFound, fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID)) }
			return c.NoContent(http.StatusNoContent)
		})
		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id"); tasksDBLock.Lock(); defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found { return xylium.NewHTTPError(http.StatusNotFound, "Task not found for completion.") }
			if task.Completed { return c.JSON(http.StatusOK, task) }
			task.Completed = true; task.UpdatedAt = time.Now().UTC(); tasksDB[taskID] = task
			return c.JSON(http.StatusOK, task)
		})
	}

	// --- Grup Rute untuk Area Admin (Menggunakan Basic Auth) ---
	adminGroup := router.Group("/admin")
	basicAuthValidator := func(username, password string, c *xylium.Context) (interface{}, bool, error) {
		if username == "admin" && password == "s3cr3tP@sswOrd" {
			return xylium.M{"username": username, "role": "administrator", "auth_time": time.Now()}, true, nil // PERBAIKAN: xylium.M
		}
		return nil, false, nil
	}
	adminGroup.Use(xylium.BasicAuthWithConfig(xylium.BasicAuthConfig{
		Validator: basicAuthValidator,
		Realm:     "Secure Admin Area",
	}))
	{
		adminGroup.GET("/dashboard", func(c *xylium.Context) error {
			userVal, _ := c.Get("user")
			return c.JSON(http.StatusOK, xylium.M{ // PERBAIKAN: xylium.M
				"message":   "Welcome to the Admin Dashboard!",
				"user_info": userVal,
			})
		})
		adminGroup.POST("/settings", func(c *xylium.Context) error {
			return c.JSON(http.StatusOK, xylium.M{"message": "Settings updated successfully."}) // PERBAIKAN: xylium.M
		})
	}

	// --- Mulai Server ---
	listenAddr := ":8080"
	appLogger.Printf("Task API server starting. Listening on http://localhost%s", listenAddr)

	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		appLogger.Fatalf("FATAL: API server encountered an error: %v", err)
	}

	if closeSharedStoreFunc != nil {
		appLogger.Println("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appLogger.Printf("Error closing shared rate limiter store: %v", err)
		}
	}
	appLogger.Println("Task API server has shut down gracefully.")
}
