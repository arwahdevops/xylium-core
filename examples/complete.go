package main

import (
	"encoding/json" // Untuk unmarshal JSON secara manual (misalnya, di handler PUT)
	"fmt"           // Untuk formatting string
	"log"           // Untuk logging standar
	"net/http"      // Untuk konstanta status HTTP
	"os"            // Untuk interaksi dengan OS (misalnya, Stdout untuk logger)
	"sync"          // Untuk sinkronisasi (RWMutex)
	"time"          // Untuk manajemen waktu

	"github.com/arwahdevops/xylium-core/src/xylium" // Impor framework Xylium
	"github.com/go-playground/validator/v10"       // Untuk validasi struct
	"github.com/valyala/fasthttp"                  // Untuk konstanta level kompresi Gzip
)

// --- Model Data ---
// Task merepresentasikan sebuah tugas dalam aplikasi.
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
// Untuk kesederhanaan, kita menggunakan map in-memory.
// Di aplikasi produksi, gunakan database persisten.
var (
	tasksDB      = make(map[string]Task) // Database in-memory untuk tasks
	tasksDBLock  sync.RWMutex            // Mutex untuk melindungi akses ke tasksDB
	nextTaskID   = 1                     // Counter untuk ID task berikutnya
	taskIDPrefix = "task-"               // Prefix untuk ID task
)

// generateTaskID menghasilkan ID unik untuk task baru.
func generateTaskID() string {
	id := fmt.Sprintf("%s%d", taskIDPrefix, nextTaskID)
	nextTaskID++
	return id
}

// --- Middleware Kustom ---

// requestLoggerMiddleware adalah middleware untuk mencatat detail setiap request.
// Sebaiknya dijalankan setelah middleware RequestID agar bisa mencatat Request ID.
func requestLoggerMiddleware(logger xylium.Logger) xylium.Middleware {
	return func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			startTime := time.Now()
			err := next(c) // Panggil handler/middleware berikutnya
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

// simpleAuthMiddleware adalah middleware untuk otentikasi sederhana berbasis API Key.
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

	// Konfigurasi logger aplikasi standar
	appLogger := log.New(os.Stdout, "[TaskAPIApp] ", log.LstdFlags|log.Lshortfile)

	// Inisialisasi shared InMemoryStore untuk Rate Limiter Global
	{
		concreteStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(5 * time.Minute))
		sharedRateLimiterStore = concreteStore
		closeSharedStoreFunc = concreteStore.Close
	}

	// Konfigurasi server Xylium
	serverCfg := xylium.DefaultServerConfig()
	serverCfg.Logger = appLogger
	serverCfg.Name = "TaskManagementAPI/1.0"
	serverCfg.ReadTimeout = 30 * time.Second
	serverCfg.WriteTimeout = 30 * time.Second
	serverCfg.ShutdownTimeout = 20 * time.Second

	// Buat instance router Xylium
	router := xylium.NewWithConfig(serverCfg)

	// --- Pendaftaran Middleware Global (Urutan Eksekusi Penting!) ---
	// Middleware dieksekusi dalam urutan ditambahkan.

	// 1. RequestID: Menambahkan ID unik ke setiap request. Penting untuk tracing dan logging.
	router.Use(xylium.RequestID()) // Menggunakan konfigurasi default

	// 2. Request Logger: Mencatat detail setiap request. Dijalankan setelah RequestID agar ID bisa dicatat.
	router.Use(requestLoggerMiddleware(appLogger))

	// 3. Timeout: Menerapkan batas waktu eksekusi untuk handler.
	//    Jika ditempatkan di sini, log awal dari RequestLogger akan tetap ada meskipun request timeout.
	//    Error timeout akan ditangani oleh ErrorHandler middleware Timeout atau GlobalErrorHandler Xylium.
	router.Use(xylium.TimeoutWithConfig(xylium.TimeoutConfig{
		Timeout: 5 * time.Second, // Batas waktu 5 detik
		Message: "The server is taking too long to respond to your request.",
		// ErrorHandler: func(c *xylium.Context, err error) error { // Contoh ErrorHandler kustom
		// 	 c.router.Logger().Printf("Custom timeout handler triggered: %v for path %s", err, c.Path())
		// 	 return c.String(http.StatusGatewayTimeout, "Request timed out (custom message).")
		// },
	}))

	// 4. CORS (Cross-Origin Resource Sharing): Mengelola request dari domain berbeda.
	//    Penting untuk API yang diakses oleh frontend web.
	router.Use(xylium.CORSWithConfig(xylium.CORSConfig{
		AllowOrigins:     []string{"http://localhost:3000", "https://myfrontend.example.com"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", xylium.DefaultRequestIDHeader},
		ExposeHeaders:    []string{"Content-Length", xylium.DefaultRequestIDHeader},
		AllowCredentials: true,
		MaxAge:           3600, // Cache preflight selama 1 jam
	}))

	// 5. Gzip Compression: Mengompresi body respons untuk mengurangi ukuran transfer.
	//    Ditempatkan setelah handler utama dan sebelum respons dikirim.
	router.Use(xylium.GzipWithConfig(xylium.GzipConfig{
		Level:     fasthttp.CompressBestSpeed, // Prioritaskan kecepatan kompresi
		MinLength: 1024,                        // Hanya kompres jika body > 1KB
		// ContentTypes: []string{"application/json", "text/html"}, // Contoh kustomisasi tipe konten
	}))

	// 6. Rate Limiter Global: Membatasi jumlah request secara umum untuk melindungi server.
	globalRateLimiterConfig := xylium.RateLimiterConfig{
		MaxRequests:    100,
		WindowDuration: 1 * time.Minute,
		Store:          sharedRateLimiterStore, // Menggunakan store yang di-share
		Skip: func(c *xylium.Context) bool {
			return c.Path() == "/health" // Lewati untuk endpoint /health
		},
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

	// 7. Middleware Keamanan Tambahan: Menambahkan header keamanan HTTP umum.
	//    Biasanya salah satu middleware terakhir sebelum handler aplikasi.
	router.Use(func(next xylium.HandlerFunc) xylium.HandlerFunc {
		return func(c *xylium.Context) error {
			c.SetHeader("X-Content-Type-Options", "nosniff")
			c.SetHeader("X-Frame-Options", "DENY")
			c.SetHeader("X-XSS-Protection", "1; mode=block")
			return next(c)
		}
	})

	// --- Pendaftaran Rute Aplikasi ---

	// Endpoint Health Check
	router.GET("/health", func(c *xylium.Context) error {
		healthStatus := map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"uptime":    time.Since(startupTime).String(),
		}
		return c.JSON(http.StatusOK, healthStatus)
	})

	// Endpoint Contoh untuk Binding Query Params
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
		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "Filter parameters received successfully.",
			"filters": req,
		})
	})

	// Grup Rute untuk API v1
	apiV1Group := router.Group("/api/v1")
	// Terapkan middleware otentikasi untuk semua rute di dalam grup /api/v1
	// Ini akan berjalan setelah middleware global.
	apiV1Group.Use(simpleAuthMiddleware("mysecretapikey123"))

	// Sub-grup untuk endpoint Tasks
	tasksAPI := apiV1Group.Group("/tasks")
	{
		// Handler untuk membuat task baru (POST /api/v1/tasks)
		postTaskHandler := func(c *xylium.Context) error {
			var req struct {
				Title       string     `json:"title" validate:"required,min=3,max=200"`
				Description string     `json:"description,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty"`
				Tags        []string   `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
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
		}

		// Konfigurasi Rate Limiter khusus untuk endpoint pembuatan task.
		// Menggunakan store baru agar perilakunya independen dari global limiter.
		// CATATAN: Lifecycle Close() untuk store ini perlu dikelola jika aplikasi lebih kompleks.
		createTaskLimiterStore := xylium.NewInMemoryStore(xylium.WithCleanupInterval(2 * time.Minute))
		// defer createTaskLimiterStore.Close() // Jika ingin Close saat main() selesai (tidak ideal untuk handler)

		createTaskRateLimiterConfig := xylium.RateLimiterConfig{
			MaxRequests:    5,
			WindowDuration: 1 * time.Minute,
			Store:          createTaskLimiterStore,
			Message:        "You are attempting to create tasks too frequently. Please wait a moment.",
			KeyGenerator: func(c *xylium.Context) string {
				// simpleAuthMiddleware sudah berjalan (karena ini middleware route, dan auth di grup)
				apiKey := c.Header("X-API-Key")
				return "task_create_limit:" + c.RealIP() + ":" + apiKey
			},
			SendRateLimitHeaders: xylium.SendHeadersOnLimit,
		}
		// Terapkan middleware RateLimiter ini hanya untuk rute POST /api/v1/tasks
		// Ini akan berjalan setelah middleware global dan middleware grup.
		tasksAPI.POST("", postTaskHandler, xylium.RateLimiter(createTaskRateLimiterConfig))

		// Handler untuk mendapatkan semua task (GET /api/v1/tasks)
		tasksAPI.GET("", func(c *xylium.Context) error {
			tasksDBLock.RLock()
			defer tasksDBLock.RUnlock()
			taskList := make([]Task, 0, len(tasksDB))
			for _, task := range tasksDB {
				taskList = append(taskList, task)
			}
			return c.JSON(http.StatusOK, taskList)
		})

		// Handler untuk mendapatkan task berdasarkan ID (GET /api/v1/tasks/:id)
		tasksAPI.GET("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.RLock()
			task, found := tasksDB[taskID]
			tasksDBLock.RUnlock()
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found.", taskID))
			}
			return c.JSON(http.StatusOK, task)
		})

		// Handler untuk memperbarui task berdasarkan ID (PUT /api/v1/tasks/:id)
		tasksAPI.PUT("/:id", func(c *xylium.Context) error {
			taskID := c.Param("id")
			var rawRequest map[string]json.RawMessage
			bodyBytes := c.Body()
			if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Invalid JSON body for update.").WithInternal(err)
			}
			_, dueDateInRequest := rawRequest["due_date"]
			_, tagsInRequest := rawRequest["tags"]
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			existingTask, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound,
					fmt.Sprintf("Task with ID '%s' not found for update.", taskID))
			}
			var req struct {
				Title       *string    `json:"title,omitempty" validate:"omitempty,min=3,max=200"`
				Description *string    `json:"description,omitempty"`
				Completed   *bool      `json:"completed,omitempty"`
				DueDate     *time.Time `json:"due_date,omitempty" validate:"omitempty"`
				Tags        *[]string  `json:"tags,omitempty" validate:"omitempty,dive,min=2,max=10"`
			}
			if err := json.Unmarshal(bodyBytes, &req); err != nil {
				return xylium.NewHTTPError(http.StatusBadRequest, "Failed to parse JSON for update.").WithInternal(err)
			}
			currentValidator := xylium.GetValidator()
			if err := currentValidator.Struct(&req); err != nil {
				if vErrs, ok := err.(validator.ValidationErrors); ok {
					errFields := make(map[string]string)
					for _, fe := range vErrs {
						errFields[fe.Field()] = fmt.Sprintf("Validation failed on '%s' tag (value: '%v').", fe.Tag(), fe.Value())
						if fe.Param() != "" {
							errFields[fe.Field()] += fmt.Sprintf(" Param: %s.", fe.Param())
						}
					}
					return xylium.NewHTTPError(xylium.StatusBadRequest, map[string]interface{}{"message": "Validation failed during update.", "details": errFields}).WithInternal(err)
				}
				return xylium.NewHTTPError(xylium.StatusBadRequest, "Validation processing error during update.").WithInternal(err)
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
				} else {
					if req.DueDate.Before(time.Now().UTC().Truncate(24*time.Hour)) {
						return xylium.NewHTTPError(http.StatusBadRequest,
							map[string]string{"due_date": "Due date must be today or in the future if provided for update."})
					}
					existingTask.DueDate = req.DueDate
				}
				changed = true
			}
			if tagsInRequest {
				if req.Tags == nil {
					existingTask.Tags = nil
				} else {
					existingTask.Tags = *req.Tags
				}
				changed = true
			}
			if changed {
				existingTask.UpdatedAt = time.Now().UTC()
				tasksDB[taskID] = existingTask
			}
			return c.JSON(http.StatusOK, existingTask)
		})

		// Handler untuk menghapus task berdasarkan ID (DELETE /api/v1/tasks/:id)
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
					fmt.Sprintf("Task with ID '%s' not found for deletion.", taskID))
			}
			return c.NoContent(http.StatusNoContent)
		})

		// Handler untuk menandai task sebagai selesai (PATCH /api/v1/tasks/:id/complete)
		tasksAPI.PATCH("/:id/complete", func(c *xylium.Context) error {
			taskID := c.Param("id")
			tasksDBLock.Lock()
			defer tasksDBLock.Unlock()
			task, found := tasksDB[taskID]
			if !found {
				return xylium.NewHTTPError(http.StatusNotFound, "Task not found for completion.")
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

	// --- Mulai Server ---
	listenAddr := ":8080"
	appLogger.Printf("Task API server starting. Listening on http://localhost%s", listenAddr)

	// Gunakan ListenAndServeGracefully untuk shutdown yang aman
	if err := router.ListenAndServeGracefully(listenAddr); err != nil {
		appLogger.Fatalf("FATAL: API server encountered an error: %v", err)
	}

	// Setelah server berhenti, panggil fungsi Close untuk store RateLimiter yang di-share
	if closeSharedStoreFunc != nil {
		appLogger.Println("Closing shared rate limiter store...")
		if err := closeSharedStoreFunc(); err != nil {
			appLogger.Printf("Error closing shared rate limiter store: %v", err)
		}
	}
	// CATATAN: Lifecycle Close() untuk `createTaskLimiterStore` (yang dibuat ad-hoc) tidak dikelola di sini.
	// Untuk aplikasi produksi, semua resource stateful harus memiliki mekanisme cleanup yang jelas.

	appLogger.Println("Task API server has shut down gracefully.")
}
