**Contoh Sintaks Xylium Minimal untuk Kasus Umum:**

**1. Server "Hello World" Paling Dasar:**

```go
package main

import (
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New() // Default ke DebugMode (setelah penyesuaian mode.go)

	app.GET("/", func(c *xylium.Context) error {
		return c.String(http.StatusOK, "Hello, Xylium!")
	})

	// Menggunakan logger Xylium untuk pesan startup (lebih baik dari stlog langsung)
	// Jika Anda belum mengkonfigurasi logger khusus di DefaultServerConfig,
	// ini akan menggunakan fallback logger Xylium.
	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err) // Gunakan log.Fatalf untuk error fatal saat startup
	}
}
```
*   **Minimal:** Inisialisasi, satu rute, satu respons string, dan server start.
*   **DX:** Sangat mirip dengan framework lain.

**2. Rute dengan Parameter Path:**

```go
package main

import (
	"fmt"
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	app.GET("/hello/:name", func(c *xylium.Context) error {
		name := c.Param("name") // Mengambil parameter path
		return c.String(http.StatusOK, "Hello, %s!", name)
	})

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Fokus pada `c.Param()`.
*   **DX:** Intuitif.

**3. Mengambil Query Parameter:**

```go
package main

import (
	"fmt"
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	// Contoh: /search?query=xylium&limit=10
	app.GET("/search", func(c *xylium.Context) error {
		query := c.QueryParam("query")             // Mengambil query param "query"
		limit := c.QueryParamIntDefault("limit", 10) // Mengambil "limit" sebagai int, default 10

		// Lakukan sesuatu dengan query dan limit
		return c.JSON(http.StatusOK, xylium.M{
			"searched_for": query,
			"limit_is":     limit,
		})
	})

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Fokus pada `c.QueryParam()` dan `c.QueryParamIntDefault()`.
*   **DX:** Jelas dan menyediakan helper untuk tipe data.

**4. Binding Request Body (JSON) ke Struct dan Validasi:**

```go
package main

import (
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Definisikan struct untuk request body
type CreateUserInput struct {
	Username string `json:"username" validate:"required,min=3"`
	Email    string `json:"email" validate:"required,email"`
}

func main() {
	app := xylium.New()

	app.POST("/users", func(c *xylium.Context) error {
		var input CreateUserInput

		// Bind request body JSON ke struct 'input' dan validasi
		if err := c.BindAndValidate(&input); err != nil {
			// err sudah berupa *xylium.HTTPError (biasanya 400 Bad Request dengan detail validasi)
			// GlobalErrorHandler akan menanganinya.
			return err
		}

		// Proses input yang sudah valid
		return c.JSON(http.StatusCreated, xylium.M{
			"message": "User created successfully",
			"user":    input,
		})
	})

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Fokus pada `c.BindAndValidate()`.
*   **DX:** Sangat ringkas untuk operasi umum binding & validasi.

**5. Middleware Sederhana (Global):**

```go
package main

import (
	"net/http"
	"log"
	"time"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Middleware logger sederhana
func LoggerMiddleware(next xylium.HandlerFunc) xylium.HandlerFunc {
	return func(c *xylium.Context) error {
		start := time.Now()
		// Panggil handler berikutnya
		err := next(c)
		// Setelah handler selesai
		log.Printf(
			"[%s] %s %s %d %s",
			c.Method(),
			c.Path(),
			c.RealIP(),
			c.Ctx.Response.StatusCode(), // Akses status code dari fasthttp context
			time.Since(start),
		)
		return err // Kembalikan error dari handler (jika ada)
	}
}

func main() {
	app := xylium.New()

	// Terapkan middleware global
	app.Use(LoggerMiddleware)

	app.GET("/ping", func(c *xylium.Context) error {
		return c.JSON(http.StatusOK, xylium.M{"message": "pong"})
	})

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Menunjukkan pola dasar middleware.
*   **DX:** Pola standar yang mudah dipahami.

**6. Grup Rute (Route Grouping):**

```go
package main

import (
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

func main() {
	app := xylium.New()

	// Grup untuk API v1
	v1 := app.Group("/api/v1")
	{ // Kurung kurawal opsional, hanya untuk visual grouping di kode
		v1.GET("/users", func(c *xylium.Context) error {
			return c.JSON(http.StatusOK, []xylium.M{
				{"id": 1, "name": "Alice"},
				{"id": 2, "name": "Bob"},
			})
		})

		v1.GET("/products", func(c *xylium.Context) error {
			return c.JSON(http.StatusOK, []xylium.M{
				{"id": 101, "name": "Laptop"},
				{"id": 102, "name": "Mouse"},
			})
		})
	}

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Menunjukkan cara mengelompokkan rute.
*   **DX:** API `Group()` yang jelas.

**7. Mengembalikan Error dari Handler:**

```go
package main

import (
	"errors"
	"net/http"
	"log"

	"github.com/arwahdevops/xylium-core/src/xylium"
)

// Fungsi service tiruan
func findUserByID(id string) (xylium.M, error) {
	if id == "1" {
		return xylium.M{"id": 1, "name": "Alice"}, nil
	}
	return nil, errors.New("user not found in service") // Error generik
}

func main() {
	app := xylium.New()

	app.GET("/users/:id", func(c *xylium.Context) error {
		userID := c.Param("id")
		user, err := findUserByID(userID)

		if err != nil {
			// Jika service mengembalikan error, kita bisa:
			// 1. Mengembalikan error generik (GlobalErrorHandler akan menangani sebagai 500 default)
			//    return err

			// 2. Atau, membuat HTTPError spesifik
			return xylium.NewHTTPError(http.StatusNotFound, "User with ID "+userID+" was not found.")
		}

		return c.JSON(http.StatusOK, user)
	})

	app.Logger().Printf("Server starting on :8080")
	if err := app.ListenAndServeGracefully(":8080"); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
```
*   **Minimal:** Menunjukkan cara mengembalikan `error` standar atau `*xylium.HTTPError`.
*   **DX:** Pola error handling yang fleksibel.

**Menambahkan `app.Start()` (Perubahan Potensial pada Xylium Core):**

Jika Anda ingin menambahkan alias `app.Start(":8080")` ke Xylium, Anda perlu memodifikasi `router_server.go` di Xylium core:

```go
// Di src/xylium/router_server.go (Xylium Core)

// ... (fungsi ListenAndServeGracefully yang sudah ada)

// Start is a convenience alias for ListenAndServeGracefully.
// It starts an HTTP server on the given network address and handles
// OS signals for a graceful shutdown.
func (r *Router) Start(addr string) error {
	return r.ListenAndServeGracefully(addr)
}
```
Setelah menambahkan ini, pengguna bisa menggunakan `app.Start(":8080")`. Ini adalah perubahan API kecil yang bisa meningkatkan kesan "kesederhanaan" untuk startup server.

**Kunci untuk Dokumentasi Minimalis:**

*   **Fokus pada Satu Konsep:** Setiap contoh harus fokus pada satu atau dua fitur inti.
*   **Kode Ringkas:** Hindari logika bisnis yang kompleks atau konfigurasi yang berlebihan di contoh awal.
*   **Asumsi Default:** Manfaatkan default Xylium (seperti mode Debug) untuk mengurangi boilerplate di contoh awal.
*   **Komentar yang Jelas dan Singkat:** Jelaskan *mengapa* kode itu ada, bukan hanya *apa* yang dilakukannya.

Dengan menyajikan contoh-contoh seperti ini di bagian awal dokumentasi Anda, pengguna baru akan lebih cepat memahami inti dari Xylium dan merasa bahwa framework ini mudah untuk digunakan dan dipelajari.
