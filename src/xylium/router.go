package xylium

import (
	"encoding/json" // Digunakan dalam ServeFiles untuk respons JSON PathNotFound
	"fmt"           // Untuk formatting error
	"io"            // Untuk HTMLRenderer
	"log"           // Untuk logger fallback jika router tidak dikonfigurasi
	"os"            // Untuk logger fallback jika router tidak dikonfigurasi
	"path/filepath" // Untuk pembersihan path di ServeFiles
	"runtime/debug" // Untuk stack trace saat panic
	"strings"       // Untuk manipulasi path

	"github.com/valyala/fasthttp" // Digunakan untuk tipe fasthttp.RequestCtx dan konstanta status
)

// HTMLRenderer mendefinisikan interface untuk rendering template HTML.
// Pengguna dapat menyediakan implementasi kustom mereka sendiri.
type HTMLRenderer interface {
	Render(w io.Writer, name string, data interface{}, c *Context) error
}

// Router adalah router utama untuk framework Xylium.
// Menyimpan tree routing, middleware global, dan handler-handler kustom.
type Router struct {
	tree             *Tree        // Radix tree untuk pencocokan rute
	globalMiddleware []Middleware // Middleware yang diterapkan ke semua rute

	// Handler kustom untuk berbagai kejadian.
	PanicHandler            HandlerFunc // Handler untuk panic yang berhasil di-recover.
	NotFoundHandler         HandlerFunc // Handler untuk rute yang tidak ditemukan (404).
	MethodNotAllowedHandler HandlerFunc // Handler untuk metode HTTP yang tidak diizinkan pada rute yang ada (405).
	GlobalErrorHandler      HandlerFunc // Handler utama untuk error yang dikembalikan oleh handler rute atau middleware.

	serverConfig ServerConfig // Konfigurasi server (didefinisikan di router_server.go).
	HTMLRenderer HTMLRenderer // Renderer HTML opsional.
}

// Logger mengembalikan logger yang dikonfigurasi untuk router.
// Mengembalikan interface xylium.Logger.
func (r *Router) Logger() Logger {
	// Pastikan logger selalu ada, meskipun serverConfig.Logger mungkin nil saat inisialisasi awal
	// (meskipun NewWithConfig seharusnya sudah menanganinya).
	if r.serverConfig.Logger == nil {
		// Fallback absolut jika logger tidak terinisialisasi dengan benar.
		// Seharusnya tidak terjadi jika New() atau NewWithConfig() digunakan.
		return log.New(os.Stderr, "[xyliumRouterFallbackLog] ", log.LstdFlags)
	}
	return r.serverConfig.Logger
}

// New membuat instance Router baru dengan konfigurasi default.
func New() *Router {
	return NewWithConfig(DefaultServerConfig()) // DefaultServerConfig dari router_server.go
}

// NewWithConfig membuat instance Router baru dengan ServerConfig yang diberikan.
func NewWithConfig(config ServerConfig) *Router {
	routerInstance := &Router{
		tree:             NewTree(),
		globalMiddleware: make([]Middleware, 0),
		serverConfig:     config,
	}

	// Atur handler default (didefinisikan di router_defaults.go)
	routerInstance.NotFoundHandler = defaultNotFoundHandler
	routerInstance.MethodNotAllowedHandler = defaultMethodNotAllowedHandler
	routerInstance.PanicHandler = defaultPanicHandler
	routerInstance.GlobalErrorHandler = defaultGlobalErrorHandler

	// Pastikan logger tidak pernah nil setelah inisialisasi router.
	if routerInstance.serverConfig.Logger == nil {
		routerInstance.serverConfig.Logger = log.New(os.Stderr, "[xyliumSrvFallbackInit] ", log.LstdFlags)
	}
	return routerInstance
}

// Use menambahkan middleware global ke router.
// Middleware ini diterapkan sebelum middleware grup atau rute spesifik.
func (r *Router) Use(middlewares ...Middleware) {
	r.globalMiddleware = append(r.globalMiddleware, middlewares...)
}

// addRoute adalah metode internal untuk mendaftarkan rute baru.
// Handler dan middleware akan ditambahkan ke tree routing.
func (r *Router) addRoute(method, path string, handler HandlerFunc, middlewares ...Middleware) {
	if path == "" {
		path = "/" // Normalisasi path kosong menjadi root
	}
	if path[0] != '/' {
		panic("xylium: path harus dimulai dengan '/'")
	}
	// Middleware yang diberikan di sini adalah middleware spesifik untuk rute ini.
	r.tree.Add(method, path, handler, middlewares...)
}

// GET mendaftarkan rute untuk metode HTTP GET.
func (r *Router) GET(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodGet, path, handler, middlewares...)
}

// POST mendaftarkan rute untuk metode HTTP POST.
func (r *Router) POST(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPost, path, handler, middlewares...)
}

// PUT mendaftarkan rute untuk metode HTTP PUT.
func (r *Router) PUT(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPut, path, handler, middlewares...)
}

// DELETE mendaftarkan rute untuk metode HTTP DELETE.
func (r *Router) DELETE(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodDelete, path, handler, middlewares...)
}

// PATCH mendaftarkan rute untuk metode HTTP PATCH.
func (r *Router) PATCH(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodPatch, path, handler, middlewares...)
}

// HEAD mendaftarkan rute untuk metode HTTP HEAD.
func (r *Router) HEAD(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodHead, path, handler, middlewares...)
}

// OPTIONS mendaftarkan rute untuk metode HTTP OPTIONS.
func (r *Router) OPTIONS(path string, handler HandlerFunc, middlewares ...Middleware) {
	r.addRoute(MethodOptions, path, handler, middlewares...)
}

// Handler adalah implementasi fasthttp.RequestHandlerFunc utama untuk router.
// Fungsi ini dipanggil oleh server fasthttp untuk setiap request yang masuk.
// Bertanggung jawab untuk:
// 1. Mengakuisisi Context dari pool.
// 2. Mencari rute yang cocok.
// 3. Membangun dan mengeksekusi chain middleware dan handler.
// 4. Menangani panic dan error yang dikembalikan.
// 5. Melepaskan Context kembali ke pool.
func (r *Router) Handler(originalFasthttpCtx *fasthttp.RequestCtx) {
	c := acquireCtx(originalFasthttpCtx)
	c.setRouter(r) // Asosiasikan router dengan context agar context bisa mengakses konfigurasi router (mis. logger, renderer).
	defer releaseCtx(c)

	var errHandler error           // Variabel untuk menyimpan error yang dikembalikan oleh chain handler.
	currentLogger := r.Logger() // Dapatkan logger yang sudah dikonfigurasi.

	defer func() {
		// Blok defer ini dieksekusi setelah seluruh chain handler selesai atau jika terjadi panic.

		// 1. Pemulihan dari Panic
		if rec := recover(); rec != nil {
			currentLogger.Printf("PANIC: %v\n%s", rec, string(debug.Stack()))
			if r.PanicHandler != nil {
				c.Set("panic_recovery_info", rec) // Simpan info panic ke context
				errHandler = r.PanicHandler(c)    // Panggil PanicHandler kustom
			} else {
				// Fallback jika PanicHandler tidak diset (seharusnya tidak terjadi jika NewWithConfig benar).
				errHandler = NewHTTPError(StatusInternalServerError, "Internal server error due to panic").WithInternal(fmt.Errorf("panic: %v", rec))
			}
		}

		// 2. Penanganan Error Global
		// Jika errHandler (dari panic atau dari return handler) tidak nil, proses melalui GlobalErrorHandler.
		if errHandler != nil {
			if !c.ResponseCommitted() { // Hanya jika respons belum dikirim ke klien.
				if r.GlobalErrorHandler != nil {
					c.Set("handler_error_cause", errHandler) // Simpan penyebab error asli ke context
					if globalErrHandlingErr := r.GlobalErrorHandler(c); globalErrHandlingErr != nil {
						// Jika GlobalErrorHandler itu sendiri gagal, ini adalah situasi kritis.
						currentLogger.Printf("CRITICAL: Error during global error handling: %v (original error: %v)", globalErrHandlingErr, errHandler)
						// Fallback absolut: kirim respons 500 sederhana.
						c.Ctx.Response.SetStatusCode(StatusInternalServerError)
						c.Ctx.Response.SetBodyString("Internal Server Error") // Jaga agar tetap sederhana
						c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
					}
				} else {
					// Fallback jika GlobalErrorHandler tidak diset (seharusnya tidak terjadi).
					currentLogger.Printf("Error (GlobalErrorHandler is nil): %v for %s %s", errHandler, c.Method(), c.Path())
					c.Ctx.Response.SetStatusCode(StatusInternalServerError)
					c.Ctx.Response.SetBodyString("Internal Server Error")
					c.Ctx.Response.Header.SetContentType("text/plain; charset=utf-8")
				}
			} else {
				// Jika respons sudah dikirim, tidak ada yang bisa dilakukan selain log error.
				currentLogger.Printf("Warning: Response already committed but an error was generated: %v for %s %s", errHandler, c.Method(), c.Path())
			}
		} else if !c.ResponseCommitted() && c.Method() != MethodHead {
			// 3. Pemeriksaan Respons (jika tidak ada error)
			// Jika chain handler selesai tanpa error, tetapi tidak ada respons yang dikirim (untuk non-HEAD request),
			// ini mungkin indikasi bug di handler.
			statusCode := c.Ctx.Response.StatusCode()
			// Respons dianggap tidak terkirim jika status code masih 0 (default fasthttp),
			// atau jika status OK (200) tetapi body kosong.
			if statusCode == 0 || (statusCode == StatusOK && len(c.Ctx.Response.Body()) == 0) {
				currentLogger.Printf("Warning: Handler chain completed for %s %s without sending a response or error.", c.Method(), c.Path())
				// Opsional: bisa mengirim respons default di sini jika diperlukan,
				// tapi biasanya lebih baik handler yang bertanggung jawab.
			}
		}
	}() // Akhir dari blok defer utama

	// Dapatkan metode dan path dari request.
	method := c.Method()
	path := c.Path()

	// Cari rute di tree.
	nodeHandler, routeMiddleware, params, allowedMethods := r.tree.Find(method, path)

	if nodeHandler != nil { // Rute ditemukan.
		c.Params = params // Set parameter rute yang diekstrak ke context.

		// Bangun chain handler: global middleware -> middleware grup (jika ada, ditangani oleh RouteGroup.addRoute) -> middleware rute -> handler utama.
		// Middleware diterapkan secara terbalik (wrapping).
		finalChain := nodeHandler
		// Terapkan middleware rute/grup (sudah dikombinasikan saat tree.Add dipanggil dari Router atau RouteGroup).
		for i := len(routeMiddleware) - 1; i >= 0; i-- {
			finalChain = routeMiddleware[i](finalChain)
		}
		// Terapkan middleware global.
		for i := len(r.globalMiddleware) - 1; i >= 0; i-- {
			finalChain = r.globalMiddleware[i](finalChain)
		}

		c.handlers = []HandlerFunc{finalChain} // Context menyimpan chain yang sudah jadi (hanya satu fungsi terkomposisi).
		c.index = -1                           // Reset indeks untuk c.Next().
		errHandler = c.Next()                  // Eksekusi chain handler.
	} else { // Rute tidak ditemukan.
		if len(allowedMethods) > 0 { // Path ada, tetapi metode HTTP tidak diizinkan.
			c.Params = params // Parameter mungkin berguna untuk MethodNotAllowedHandler.
			if r.MethodNotAllowedHandler != nil {
				c.SetHeader("Allow", strings.Join(allowedMethods, ", ")) // Set header "Allow" sesuai RFC.
				errHandler = r.MethodNotAllowedHandler(c)
			} else {
				errHandler = NewHTTPError(StatusMethodNotAllowed, StatusText(StatusMethodNotAllowed))
			}
		} else { // Path tidak ada sama sekali.
			if r.NotFoundHandler != nil {
				errHandler = r.NotFoundHandler(c)
			} else {
				errHandler = NewHTTPError(StatusNotFound, StatusText(StatusNotFound))
			}
		}
	}
}

// ServeFiles menyajikan file statis dari direktori root yang diberikan pada path prefix tertentu.
// Contoh: r.ServeFiles("/static", "./public_html") akan menyajikan file dari "./public_html"
// ketika request datang ke path yang dimulai dengan "/static".
func (r *Router) ServeFiles(pathPrefix string, rootDir string) {
	if strings.Contains(pathPrefix, ":") || strings.Contains(pathPrefix, "*") {
		panic("xylium: pathPrefix untuk ServeFiles tidak boleh mengandung ':' atau '*'")
	}
	fileSystemRoot := filepath.Clean(rootDir) // Bersihkan path root direktori.
	paramName := "filepath"                   // Nama parameter catch-all untuk subpath file.

	// Normalisasi pathPrefix.
	// Jika "/" atau "", berarti serve dari root ("").
	// Jika lain, pastikan dimulai dengan "/" dan tidak diakhiri dengan "/".
	if pathPrefix == "/" || pathPrefix == "" {
		pathPrefix = ""
	} else {
		pathPrefix = "/" + strings.Trim(pathPrefix, "/")
	}

	// Buat path rute dengan parameter catch-all.
	// Contoh: "/static/*filepath" atau "/*filepath" jika pathPrefix adalah root.
	routePath := pathPrefix + "/*" + paramName
	if pathPrefix == "" {
		routePath = "/*" + paramName
	}

	// Dapatkan logger dari router untuk digunakan dalam callback PathNotFound.
	frameworkLogger := r.Logger()

	// Konfigurasi fasthttp.FS.
	fs := &fasthttp.FS{
		Root:               fileSystemRoot,
		IndexNames:         []string{"index.html"}, // File yang dicari jika path adalah direktori.
		GenerateIndexPages: false,                  // Biasanya false untuk API; tidak membuat daftar direktori.
		AcceptByteRange:    true,                   // Izinkan request byte range.
		Compress:           true,                   // Izinkan fasthttp mengompres file (jika klien mendukung).
		PathNotFound: func(originalFasthttpCtx *fasthttp.RequestCtx) {
			// Callback ini dipanggil oleh fasthttp.FS jika file tidak ditemukan.
			// Kita akan mengirim respons JSON 404.
			errorMsg := M{"error": "File not found in static assets"}
			he := NewHTTPError(StatusNotFound, errorMsg)

			originalFasthttpCtx.SetStatusCode(he.Code)
			originalFasthttpCtx.SetContentType("application/json; charset=utf-8")

			// Encode pesan error ke body respons.
			if err := json.NewEncoder(originalFasthttpCtx.Response.BodyWriter()).Encode(he.Message); err != nil {
				// Jika encoding gagal, log error ini menggunakan logger framework.
				if frameworkLogger != nil {
					frameworkLogger.Printf(
						"xylium: Error encoding JSON for PathNotFound (ServeFiles) for URI %s: %v. Client received 404 but body might be incomplete.",
						originalFasthttpCtx.RequestURI(), // URI yang diminta klien yang menyebabkan PathNotFound.
						err,
					)
				} else {
					// Fallback jika logger framework entah bagaimana nil.
					log.Printf(
						"[xyliumSrvFilesFallback] Error encoding JSON for PathNotFound (ServeFiles) for URI %s: %v.",
						originalFasthttpCtx.RequestURI(),
						err,
					)
				}
				// Pada titik ini, header mungkin sudah terkirim sebagian.
				// Lebih aman untuk tidak mencoba memodifikasi body lebih lanjut.
			}
		},
	}
	fileHandler := fs.NewRequestHandler() // Dapatkan handler dari fasthttp.FS.

	// Daftarkan rute GET untuk path file statis.
	r.GET(routePath, func(c *Context) error {
		// Dapatkan subpath file dari parameter rute.
		requestedFileSubPath := c.Param(paramName)

		// Path yang akan diberikan ke fileHandler harus relatif terhadap FS.Root
		// dan biasanya dimulai dengan '/'.
		pathForFS := requestedFileSubPath
		if len(pathForFS) > 0 && pathForFS[0] != '/' {
			pathForFS = "/" + pathForFS
		} else if len(pathForFS) == 0 {
			// Jika requestedFileSubPath kosong (misalnya, request ke "/static/"),
			// maka set pathForFS ke "/" agar fasthttp.FS mencari index file.
			pathForFS = "/"
		}

		// Simpan URI asli request sebelum diubah. Ini bisa berguna jika ada logic
		// setelah fileHandler yang membutuhkannya, atau untuk logging.
		// originalRequestURI := make([]byte, len(c.Ctx.Request.Header.RequestURI()))
		// copy(originalRequestURI, c.Ctx.Request.Header.RequestURI())

		// Ubah sementara RequestURI pada fasthttp.RequestCtx agar sesuai dengan yang diharapkan oleh fileHandler.
		c.Ctx.Request.SetRequestURI(pathForFS)

		// Biarkan fasthttp.FS menangani request.
		fileHandler(c.Ctx)

		// Mengembalikan RequestURI ke nilai aslinya biasanya tidak diperlukan karena
		// Context akan di-reset dari pool untuk request berikutnya.
		// Namun, jika ada middleware atau logic yang berjalan SETELAH ini dalam handler yang sama
		// dan bergantung pada RequestURI asli, maka perlu dikembalikan.
		// c.Ctx.Request.SetRequestURIBytes(originalRequestURI)

		// Handler fasthttp.FS sudah menangani respons, jadi kembalikan nil.
		return nil
	})
}

// --- Route Grouping ---

// RouteGroup memungkinkan pengelompokan rute dengan path prefix dan/atau middleware yang sama.
type RouteGroup struct {
	router     *Router      // Referensi ke instance Router utama.
	prefix     string       // Path prefix untuk grup ini (misalnya, "/api/v1").
	middleware []Middleware // Middleware yang spesifik untuk grup ini.
}

// Group membuat RouteGroup baru dari Router utama.
// Semua rute yang ditambahkan ke grup ini akan diawali dengan `prefix`.
// `middlewares` yang diberikan akan diterapkan ke semua rute dalam grup ini.
func (r *Router) Group(prefix string, middlewares ...Middleware) *RouteGroup {
	// Normalisasi prefix: pastikan dimulai dengan '/' dan tidak diakhiri dengan '/',
	// kecuali jika prefix adalah root ("/" atau "").
	normalizedPrefix := ""
	if prefix != "" && prefix != "/" {
		normalizedPrefix = "/" + strings.Trim(prefix, "/")
	} else if prefix == "/" {
		// Jika prefix eksplisit "/", biarkan sebagai string kosong untuk konsistensi
		// karena root ("") dan "/" diperlakukan sama dalam pembentukan path.
		// Jika tidak, "/api" + "/" -> "//api" jika tidak hati-hati.
		normalizedPrefix = "" // Atau bisa juga "/" jika tree handling mengizinkan.
		                      // Dengan `strings.Trim(path, "/")` di `splitPath`, `""` dan `"/"` jadi `[]string{}`
	}


	// Salin slice middleware untuk menghindari modifikasi eksternal.
	groupMiddleware := make([]Middleware, len(middlewares))
	copy(groupMiddleware, middlewares)

	return &RouteGroup{
		router:     r,
		prefix:     normalizedPrefix,
		middleware: groupMiddleware,
	}
}

// Use menambahkan middleware ke RouteGroup.
// Middleware ini diterapkan setelah middleware global router dan middleware grup parent (jika ada),
// dan sebelum middleware spesifik rute.
func (rg *RouteGroup) Use(middlewares ...Middleware) {
	rg.middleware = append(rg.middleware, middlewares...)
}

// addRoute adalah helper internal untuk RouteGroup untuk mendaftarkan rute.
// Menggabungkan prefix grup dengan relativePath dan menerapkan middleware gabungan.
func (rg *RouteGroup) addRoute(method, relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	// Normalisasi relativePath.
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} else if relativePath == "/" && rg.prefix == "" {
		// Jika prefix grup adalah root ("") dan relativePath adalah "/",
		// maka path lengkapnya adalah "/".
		pathSegment = "/"
	} else if relativePath == "/" && rg.prefix != "" {
		// Jika prefix grup bukan root dan relativePath adalah "/",
		// maka pathSegment adalah string kosong, path lengkap akan menjadi rg.prefix.
		pathSegment = ""
	}


	// Gabungkan prefix grup dengan path segmen.
	// rg.prefix sudah dinormalisasi (dimulai dengan '/' atau kosong).
	// pathSegment sudah dinormalisasi (dimulai dengan '/' atau kosong, atau "/" tunggal).
	fullPath := rg.prefix + pathSegment
	if fullPath == "" { // Jika keduanya (prefix dan segment) efektif kosong.
		fullPath = "/"
	} else if strings.HasPrefix(fullPath, "//") { // Hindari double slash jika prefix tidak kosong dan pathSegment dimulai dengan /
		fullPath = strings.TrimPrefix(fullPath, "/")
		if fullPath == "" { // Jika hasilnya jadi kosong (misal prefix "/" dan pathSegment "/"), jadikan "/"
			fullPath = "/"
		} else {
			fullPath = "/" + fullPath
		}
	}


	// Gabungkan middleware grup dengan middleware spesifik rute.
	allGroupAndRouteMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, rg.middleware...) // Middleware grup dulu
	allGroupAndRouteMiddleware = append(allGroupAndRouteMiddleware, middlewares...)   // Lalu middleware rute

	// Daftarkan rute ke router utama.
	rg.router.addRoute(method, fullPath, handler, allGroupAndRouteMiddleware...)
}

// GET mendaftarkan rute HTTP GET untuk RouteGroup.
func (rg *RouteGroup) GET(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodGet, relativePath, handler, middlewares...)
}

// POST mendaftarkan rute HTTP POST untuk RouteGroup.
func (rg *RouteGroup) POST(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPost, relativePath, handler, middlewares...)
}

// PUT mendaftarkan rute HTTP PUT untuk RouteGroup.
func (rg *RouteGroup) PUT(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPut, relativePath, handler, middlewares...)
}

// DELETE mendaftarkan rute HTTP DELETE untuk RouteGroup.
func (rg *RouteGroup) DELETE(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodDelete, relativePath, handler, middlewares...)
}

// PATCH mendaftarkan rute HTTP PATCH untuk RouteGroup.
func (rg *RouteGroup) PATCH(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodPatch, relativePath, handler, middlewares...)
}

// HEAD mendaftarkan rute HTTP HEAD untuk RouteGroup.
func (rg *RouteGroup) HEAD(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodHead, relativePath, handler, middlewares...)
}

// OPTIONS mendaftarkan rute HTTP OPTIONS untuk RouteGroup.
func (rg *RouteGroup) OPTIONS(relativePath string, handler HandlerFunc, middlewares ...Middleware) {
	rg.addRoute(MethodOptions, relativePath, handler, middlewares...)
}

// Group membuat sub-grup dari RouteGroup yang sudah ada.
// Sub-grup akan mewarisi prefix dan middleware dari grup parent.
func (rg *RouteGroup) Group(relativePath string, middlewares ...Middleware) *RouteGroup {
	// Normalisasi path segmen untuk sub-grup.
	pathSegment := ""
	if relativePath != "" && relativePath != "/" {
		pathSegment = "/" + strings.Trim(relativePath, "/")
	} // Jika relativePath adalah "/" atau "", pathSegment tetap kosong, prefix baru akan sama dengan prefix parent.

	newPrefix := rg.prefix + pathSegment
	if newPrefix == "" {
		newPrefix = "/" // Jika keduanya (prefix parent dan segment) efektif kosong.
	} else if strings.HasPrefix(newPrefix, "//") {
		newPrefix = strings.TrimPrefix(newPrefix, "/")
		if newPrefix == "" {
			newPrefix = "/"
		} else {
			newPrefix = "/" + newPrefix
		}
	}


	// Gabungkan middleware grup parent dengan middleware sub-grup baru.
	combinedMiddleware := make([]Middleware, 0, len(rg.middleware)+len(middlewares))
	combinedMiddleware = append(combinedMiddleware, rg.middleware...) // Warisi middleware parent.
	combinedMiddleware = append(combinedMiddleware, middlewares...)   // Tambahkan middleware baru.

	return &RouteGroup{
		router:     rg.router, // Instance Router utama tetap sama.
		prefix:     newPrefix,
		middleware: combinedMiddleware,
	}
}
