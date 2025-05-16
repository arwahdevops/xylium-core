package main

import (
	"encoding/json" // Diperlukan untuk unmarshal manual jika ingin cek keberadaan field JSON
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/arwahdevops/xylium-core/xylium"
)

// --- Model Data untuk Task ---
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title" validate:"required,min=3,max=200"`
	Description string     `json:"description,omitempty"`
	Completed   bool       `json:"completed"`
	DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty,gt"`
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
			if err != nil {
				logger.Printf("Error processing request %s %s: %v", c.Method(), c.Path(), err)
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

var startupTime time.Time // <<< PERBAIKAN: Deklarasi startupTime di scope package

func main() {
	startupTime = time.Now().UTC() // <<< PERBAIKAN: Inisialisasi startupTime

	appLogger := log.New(os.Stdout, "[TaskAPIApp] ", log.LstdFlags|log.Lshortfile)

	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger
	serverCfg.Name = "TaskManagementAPI/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second

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
			"uptime":    time.Since(startupTime).String(), // <<< PERBAIKAN: Sekarang startupTime terdefinisi
		}
		return c.JSON(http.StatusOK, healthStatus)
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
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty"`
			}
			if err := c.BindAndValidate(&req); err != nil {
				return err
			}
			if req.DueDate != nil && req.DueDate.Before(time.Now().UTC()) {
				return xylium.NewHTTPError(http.StatusBadRequest,
					map[string]string{"due_date": "Due date must be in the future."})
			}
			now := time.Now().UTC()
			newTask := Task{
				ID:          generateTaskID(),
				Title:       req.Title,
				Description: req.Description,
				Completed:   false,
				DueDate:     req.DueDate,
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

			// Untuk PUT, kita perlu tahu field mana yang benar-benar dikirim oleh klien
			// untuk membedakan antara "tidak dikirim" dan "dikirim sebagai null".
			// Cara paling robust adalah dengan unmarshal ke map[string]interface{} terlebih dahulu
			// atau menggunakan struct dengan pointer ke semua field yang bisa null/opsional.
			// Pendekatan saat ini dengan struct pointer sudah cukup baik untuk field opsional.

			// Pertama, unmarshal ke map untuk memeriksa keberadaan field 'due_date' secara eksplisit
			var rawRequest map[string]json.RawMessage
			if err := json.Unmarshal(c.Body(), &rawRequest); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Invalid JSON body").WithInternal(err)
			}
			_, dueDateInRequest := rawRequest["due_date"]


			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()

			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found to update", taskID))
			}

			// Struct untuk binding data yang sebenarnya, setelah kita tahu field mana yang ada
			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty"`
			}

			// Bind ke struct req. Ini akan mengisi field yang ada di JSON.
			// Jika due_date ada dan null di JSON, req.DueDate akan menjadi nil.
			// Jika due_date tidak ada di JSON, req.DueDate juga akan menjadi nil.
			// Kita membedakannya dengan flag dueDateInRequest.
			if err := json.Unmarshal(c.Body(), &req); err != nil { // Unmarshal lagi ke struct
				// Sebenarnya Bind() akan melakukan ini, tapi kita sudah unmarshal ke map.
				// Untuk konsistensi, kita bisa panggil BindAndValidate saja, tapi itu akan membaca body lagi.
				// Pilihan lain: Gunakan `json.NewDecoder(bytes.NewReader(c.Body())).Decode(&req)`
				// Untuk contoh ini, kita re-unmarshal ke struct.
				// Atau lebih baik: Panggil BindAndValidate SETELAH cek manual.
				// Mari kita sederhanakan untuk saat ini:
				// Panggil Bind, lalu cek due_date_in_request.
				// Untuk lebih baik, Bind harus bisa memberi tahu apakah field ada.
				// Kita pakai pendekatan `BindAndValidate` lalu cek `dueDateInRequest`.
				// Jika BindAndValidate dipanggil, ia akan membaca body. Jadi rawRequest harus di-unmarshal dari body yang sama.
				// Ini menjadi sedikit rumit jika ingin efisien dan robust.
				// Mari kita stick dengan BindAndValidate dulu, lalu cek `dueDateInRequest` dari map sebelumnya.
				if errBind := c.BindAndValidate(&req); errBind != nil {
					return errBind
				}
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

			// <<< PERBAIKAN LOGIKA DueDate untuk PUT >>>
			if dueDateInRequest { // Hanya proses jika 'due_date' ada di payload JSON
				if req.DueDate == nil { // Klien mengirim "due_date": null
					existingTask.DueDate = nil
					changed = true
				} else { // Klien mengirim "due_date": "some-date"
					if req.DueDate.Before(time.Now().UTC()) {
						return xylium.NewHTTPError(http.StatusBadRequest,
							map[string]string{"due_date": "Due date must be in the future if provided."})
					}
					existingTask.DueDate = req.DueDate
					changed = true
				}
			} // Jika dueDateInRequest false, kita tidak menyentuh existingTask.DueDate

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
	if err := router.ListenAndServe(listenAddr); err != nil {
		appLogger.Fatalf("FATAL: Failed to start API server: %v", err)
	}
}
