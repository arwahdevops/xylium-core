
---

**Konten Dokumentasi: `Docs/ErrorHandling.md` (Baru) atau Tambahan untuk `Cookbook.md`**

```markdown
# Penanganan Error di Xylium

Xylium menyediakan mekanisme penanganan error yang fleksibel dan terpusat, memungkinkan Anda mengelola error aplikasi secara konsisten dan memberikan respons yang sesuai kepada klien.

## Dasar-Dasar Error Handling

Handler rute dan middleware di Xylium memiliki signature `func(c *xylium.Context) error`.

*   Jika handler mengembalikan `nil`, Xylium mengasumsikan request telah berhasil ditangani (dan respons mungkin sudah dikirim).
*   Jika handler mengembalikan `error` non-nil, error tersebut akan diteruskan ke **Global Error Handler** framework.

## `xylium.HTTPError`

Untuk kontrol penuh atas respons error HTTP, Xylium menyediakan struct `xylium.HTTPError`. Ini adalah cara standar untuk mengkomunikasikan error yang harus menghasilkan status HTTP dan pesan tertentu ke klien.

```go
// Definisi (dari xylium/errors.go)
type HTTPError struct {
    Code     int         `json:"-"`    // Kode status HTTP
    Message  interface{} `json:"error"`  // Pesan untuk klien (bisa string, xylium.M, atau struct lain)
    Internal error       `json:"-"`    // Error internal untuk logging, tidak diekspos ke klien (kecuali DebugMode)
}
```

### Membuat `HTTPError`

Gunakan fungsi `xylium.NewHTTPError(code int, message ...interface{}) *HTTPError`:

1.  **Error Sederhana dengan Pesan String:**
    ```go
    // Di dalam handler Anda:
    if productNotFound {
        return xylium.NewHTTPError(http.StatusNotFound, "Produk yang Anda cari tidak ditemukan.")
    }
    ```
    Ini akan menghasilkan respons JSON seperti (jika `GlobalErrorHandler` default digunakan):
    ```json
    // Status: 404 Not Found
    {
        "error": "Produk yang Anda cari tidak ditemukan."
    }
    ```

2.  **Error dengan Pesan Default Berdasarkan Kode Status:**
    Jika Anda tidak menyediakan pesan, Xylium akan menggunakan teks status HTTP standar.
    ```go
    // Di dalam handler Anda:
    if someResourceIsGone {
        return xylium.NewHTTPError(http.StatusGone) // Pesan akan menjadi "Gone"
    }
    ```
    Respons JSON:
    ```json
    // Status: 410 Gone
    {
        "error": "Gone"
    }
    ```

3.  **Error dengan Pesan Terstruktur (menggunakan `xylium.M`):**
    Berguna untuk memberikan detail lebih lanjut kepada klien.
    ```go
    // Di dalam handler Anda:
    if validationFailed {
        errorDetails := xylium.M{
            "field": "email",
            "issue": "Alamat email sudah terdaftar.",
        }
        // Pesan utama bisa berupa string, dan 'details' akan menjadi bagian dari Message
        return xylium.NewHTTPError(http.StatusConflict, xylium.M{
            "message": "Registrasi pengguna gagal",
            "details": errorDetails,
        })
    }
    ```
    Respons JSON:
    ```json
    // Status: 409 Conflict
    {
        "error": { // Perhatikan bahwa 'error' sekarang adalah objek
            "message": "Registrasi pengguna gagal",
            "details": {
                "field": "email",
                "issue": "Alamat email sudah terdaftar."
            }
        }
    }
    ```
    Atau, jika Anda hanya memberikan `xylium.M` sebagai argumen kedua `NewHTTPError`:
    ```go
    return xylium.NewHTTPError(http.StatusConflict, xylium.M{"code": "EMAIL_TAKEN", "description": "Alamat email sudah terdaftar."})
    ```
    Respons JSON:
    ```json
    // Status: 409 Conflict
    {
        "error": {
             "code": "EMAIL_TAKEN",
             "description": "Alamat email sudah terdaftar."
        }
    }
    ```


4.  **Menyertakan Error Internal (`WithInternal`):**
    Sangat penting untuk logging dan debugging tanpa mengekspos detail sensitif ke klien (kecuali dalam `DebugMode`).
    ```go
    // Di dalam handler Anda:
    dbResult, dbErr := queryDatabase(...)
    if dbErr != nil {
        // Pesan generik untuk klien
        clientMessage := "Terjadi masalah saat mengakses data kami."
        // Kembalikan HTTPError dengan error database asli sebagai Internal
        return xylium.NewHTTPError(http.StatusInternalServerError, clientMessage).WithInternal(dbErr)
    }
    ```
    *   **Dalam `ReleaseMode`**, klien akan melihat:
        ```json
        // Status: 500 Internal Server Error
        {
            "error": "Terjadi masalah saat mengakses data kami."
        }
        ```
        Log server akan berisi detail `dbErr`.
    *   **Dalam `DebugMode`**, `GlobalErrorHandler` default Xylium akan menambahkan `_debug_info`:
        ```json
        // Status: 500 Internal Server Error
        {
            "error": "Terjadi masalah saat mengakses data kami.",
            "_debug_info": {
                "internal_error_details": "pq: duplicate key value violates unique constraint \"users_email_key\"" // Contoh error dari dbErr.Error()
            }
        }
        ```

5.  **Mengadopsi Error Lain ke dalam `HTTPError`:**
    `NewHTTPError` cukup pintar untuk menangani error yang ada.
    ```go
    // Misalkan someFunc() mengembalikan error standar Go
    err := someFunc()
    if err != nil {
        // Jika kita ingin error ini menjadi 400 Bad Request
        return xylium.NewHTTPError(http.StatusBadRequest, err)
        // Pesan klien akan menjadi err.Error(). Error asli akan menjadi .Internal.
    }

    // Jika someFunc() sudah mengembalikan *xylium.HTTPError
    httpErr := someFuncThatReturnsHTTPError()
    if httpErr != nil {
        // Jika kita ingin menimpanya dengan kode status baru tapi mempertahankan pesan dan internal error:
        return xylium.NewHTTPError(http.StatusServiceUnavailable, httpErr)
    }
    ```

### Mengubah Pesan `HTTPError` (`WithMessage`)

Anda dapat memodifikasi pesan dari `HTTPError` yang ada:
```go
err := xylium.NewHTTPError(http.StatusPaymentRequired, "Akses fitur premium diperlukan.")
// ... logika lain ...
if userIsTrial {
    err = err.WithMessage("Upgrade akun Anda untuk mengakses fitur ini. Masa trial Anda telah berakhir.")
}
return err
```

## Global Error Handler (`Router.GlobalErrorHandler`)

Semua error non-nil yang dikembalikan dari handler atau middleware (dan error yang dikembalikan oleh `PanicHandler`) akhirnya diproses oleh `Router.GlobalErrorHandler`.

Xylium menyediakan `defaultGlobalErrorHandler` yang:
*   Mengambil error asli dari `c.Get("handler_error_cause")`.
*   Menggunakan `c.Logger()` untuk logging kontekstual.
*   Membedakan antara `*xylium.HTTPError` dan error Go generik.
*   Mengirim respons JSON ke klien:
    *   Untuk `*xylium.HTTPError`, menggunakan `Code` dan `Message` dari error tersebut. Dalam `DebugMode`, `Internal.Error()` ditambahkan ke `_debug_info`.
    *   Untuk error generik, mengirim 500. Dalam `DebugMode`, `originalErr.Error()` ditambahkan ke `_debug_info`.

### Kustomisasi Global Error Handler

Anda dapat mengganti `defaultGlobalErrorHandler` dengan implementasi Anda sendiri saat menginisialisasi router atau setelahnya:

```go
// Di main.go atau setup aplikasi Anda
router := xylium.New() // atau NewWithConfig
router.GlobalErrorHandler = func(c *xylium.Context) error {
    errVal, _ := c.Get("handler_error_cause")
    originalErr, _ := errVal.(error)
    logger := c.Logger() // Logger Xylium kontekstual

    var httpCode int = http.StatusInternalServerError
    var clientResponse interface{}

    if he, ok := originalErr.(*xylium.HTTPError); ok {
        httpCode = he.Code
        clientResponse = he.Message
        logger.WithFields(xylium.M{"status": httpCode, "internal_error": he.Internal}).Errorf("CustomErrorHandler: HTTPError: %v", he.Message)
    } else if originalErr != nil {
        clientResponse = xylium.M{"error_code": "UNEXPECTED_ERROR", "message": "An unexpected error occurred."}
        if c.RouterMode() == xylium.DebugMode {
             clientResponse.(xylium.M)["debug_details"] = originalErr.Error()
        }
        logger.WithFields(xylium.M{"status": httpCode}).Errorf("CustomErrorHandler: Generic Error: %v", originalErr)
    } else {
        clientResponse = xylium.M{"error": "Unknown error"}
        logger.Warn("CustomErrorHandler: Called without a valid error cause.")
    }

    // Pastikan Anda tidak mengirim respons jika sudah terkirim
    if !c.ResponseCommitted() {
        return c.JSON(httpCode, clientResponse)
    }
    return nil // Respons sudah terkirim
}
```
**Penting:** Pastikan error handler kustom Anda selalu mengirim respons atau mengembalikan error jika gagal mengirim respons, untuk menghindari request menggantung.

## Penanganan Panic (`Router.PanicHandler`)

Xylium secara otomatis memulihkan dari panic yang terjadi di handler atau middleware. Setelah pemulihan, `Router.PanicHandler` dipanggil.

`defaultPanicHandler` Xylium akan:
1.  Mencatat panic (stack trace dicatat oleh Router inti sebelum memanggil panic handler).
2.  Mengembalikan `xylium.NewHTTPError` dengan status 500 dan pesan generik, dengan info panic sebagai `Internal` error.
3.  Error ini kemudian diproses oleh `GlobalErrorHandler`.

Anda juga dapat menyediakan `PanicHandler` kustom:
```go
router.PanicHandler = func(c *xylium.Context) error {
    panicInfo, _ := c.Get("panic_recovery_info")
    c.Logger().WithFields(xylium.M{"panic_value": panicInfo}).Criticalf("PANIC RECOVERED by custom handler!")
    // Mungkin kirim notifikasi ke sistem monitoring di sini

    // Kembalikan HTTPError yang akan diproses oleh GlobalErrorHandler
    return xylium.NewHTTPError(http.StatusInternalServerError, "We're experiencing technical difficulties. Please try again later.").WithInternal(fmt.Errorf("panic: %v", panicInfo))
}
```

## Error dari `c.BindAndValidate`

Metode `c.BindAndValidate(out interface{}) error` akan mengembalikan `*xylium.HTTPError` jika binding atau validasi gagal.
*   **Kegagalan Binding:** Biasanya menghasilkan 400 Bad Request dengan pesan seperti "Invalid JSON data".
*   **Kegagalan Validasi:** Menghasilkan 400 Bad Request. Pesan akan berupa `xylium.M` yang berisi `message: "Validation failed."` dan `details: map[string]string` yang memetakan nama field ke pesan error validasi spesifik.
    ```json
    // Contoh respons dari BindAndValidate jika validasi gagal
    // Status: 400 Bad Request
    {
        "error": {
            "message": "Validation failed.",
            "details": {
                "Username": "validation failed on tag 'required'",
                "Email": "validation failed on tag 'email'"
            }
        }
    }
    ```
Anda dapat langsung mengembalikan error dari `BindAndValidate` agar diproses oleh `GlobalErrorHandler`.

```go
func CreateUserHandler(c *xylium.Context) error {
    var input CreateUserInput
    if err := c.BindAndValidate(&input); err != nil {
        // Tidak perlu log di sini, GlobalErrorHandler akan log HTTPError
        return err // Kembalikan HTTPError langsung
    }
    // ... proses input ...
    return c.String(http.StatusCreated, "User created")
}
```

Dengan memahami `HTTPError` dan alur penanganan error/panic, Anda dapat membangun aplikasi Xylium yang tangguh dan memberikan feedback yang berguna baik untuk developer (melalui log) maupun pengguna (melalui respons API).
```

---
