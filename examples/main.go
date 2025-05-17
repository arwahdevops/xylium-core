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
	"github.com/go-playground/validator/v10" // PERBAIKAN: Impor validator untuk ValidationErrors
)

// --- Model Data untuk Task ---
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

// --- Penyimpanan Data Sederhana (In-memory) untuk Tasks ---
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

			logger.Printf("[%s] %s %s \"%s\" %d %s",
				c.RealIP(),
				c.Method(),
				c.Path(),
				c.UserAgent(),
				statusCode,
				latency,
			)
			return err
		}
	}
}

func simpleAuthMiddleware(validAPIKey string) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			providedKey := c.Header("X-API-Key")
			if providedKey == "" {
				return xylium.NewHTTPError(http.StatusUnauthorized, "API key is required in X-API-Key header")
			}
			if providedKey != validAPIKey {
				return xylium.NewHTTPError(http.StatusForbidden, "Invalid API key provided")
			}
			c.Set("authenticated_via", "APIKey")
			return next(c)
		}
	}
}

var startupTime time.Time

func main() {
	startupTime = time.Now().UTC()

	appLogger := log.New(os.Stdout, "[TaskAPIApp] ", log.LstdFlags|log.Lshortfile)

	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger
	serverCfg.Name = "TaskManagementAPI/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second

	router := xylium.NewWithConfig(serverCfg)
	frameworkLogger := router.Logger()

	router.Use(requestLoggerMiddleware(frameworkLogger))
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			return next(c)
		}
	})

	router.GET("/health", func(c *xylium.Context) error {
		healthStatus := map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":    time.Since(startupTime).String(),
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	type FilterRequest struct {
		StartDate *time.Time `query:"startDate"`
		EndDate   *time.Time `query:"endDate"`
		Status    []string   `query:"status" validate:"omitempty,dive,oneof=pending completed failed"`
		Priorities []int     `query:"priority" validate:"omitempty,dive,min=1,max=5"`
	}
	router.GET("/filter-tasks", func(c *xylium.Context) error {
		var req FilterRequest
		if err := c.BindAndValidate(&req); err != nil {
			return err
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "Filter parameters received",
			"filters": req,
		})
	})

	apiV1Group := router.Group("/api/v1")
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	tasksAPI := apiV1Group.Group("/tasks")
	{
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock()
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			return c.JSON(http.StatusOK, taskList)
		})

		tasksAPI.POST("", func(c *xylium.Context) error {
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}
			if err := c.BindAndValidate(&req); err != nil {
				return err
			}
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				return xylium.NewHTTPError(http.StatusBadRequest,
					map[string]string{"due_date": "Due date must be today or in the future."})
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
			tasksDBLock.Lock()
			tasksDB[newTask.ID] = newTask
			tasksDBLock.Unlock()
			return c.JSON(http.StatusCreated, newTask)
		})

		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found", taskID))
			}
			return c.JSON(http.StatusOK, task)
		})

		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")

			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body()
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Invalid JSON body").WithInternal(err)
			}
			_, dueDateInRequest := rawRequest["due_date"]
			_, tagsInRequest := rawRequest["tags"]


			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()

			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found to update", taskID))
			}

			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2"`
			}

			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Invalid JSON for update").WithInternal(err)
			}

			// PERBAIKAN: Gunakan instance validator dari xylium atau import validator
			currentValidator := xylium.GetValidator() // Atau bisa juga instance validator.New() jika tidak ada dependensi ke xylium
			if err := currentValidator.Struct(&req); err != nil {
				// PERBAIKAN: Gunakan validator.ValidationErrors dari paket validator yang diimpor
				if vErrs, ok := err.(validator.ValidationErrors); ok {
					errFields := make(map[string]string)
					for _, fe := range vErrs {
						errFields[fe.Field()] = fmt.Sprintf("validation failed on '%s' tag", fe.Tag())
						if fe.Param() != "" {
							errFields[fe.Field()] += fmt.Sprintf(" (param: %s)", fe.Param())
						}
					}
					// PERBAIKAN: Gunakan konstanta status dari paket xylium
					return xylium.NewHTTPError(xylium.StatusBadRequest, map[string]interface{}{"message": "Validation failed", "details": errFields}).WithInternal(err)
				}
				// PERBAIKAN: Gunakan konstanta status dari paket xylium
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error").WithInternal(err)
			}


			changed := false
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

			if dueDateInRequest {
				if req.DueDate == nil {
					existingTask.DueDate = nil
					changed = true
				} else {
					if req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
						return xylium.NewHTTPError(http.StatusBadRequest,
							map[string]string{"due_date": "Due date must be today or in the future if provided."})
					}
					existingTask.DueDate = req.DueDate
					changed = true
				}
			}

			if tagsInRequest {
				if req.Tags == nil {
					existingTask.Tags = nil
					changed = true
				} else {
					existingTask.Tags = *req.Tags
					changed = true
				}
			}


			if changed {
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask
			}

			return c.JSON(http.StatusOK, existingTask)
		})

		tasksAPI.DELETE("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock()
			_, found := tasksDB[taskID]
			if found {
				delete(tasksDB, taskID)
			}
			tasksDBLock.Unlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found to delete", taskID))
			}
			return c.NoContent(http.StatusNoContent)
		})

		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found")
			}
			if task.Completed {
				return c.JSON(http.StatusOK, task)
			}
			task.Completed = true
			task.UpdatedAt = time.Now().UTC()
			tasksDB[taskID] = task
			return c.JSON(http.StatusOK, task)
		})
	}

	listenAddr := ":8080"
	appLogger.Printf("Task API server starting. Listening on http://localhost%s", listenAddr)

	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		appLogger.Fatalf("FATAL: API server error: %v", err)
	}
	appLogger.Println("Task API server has shut down gracefully.")
}
