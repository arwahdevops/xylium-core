// src/xylium/middleware_compress.go

package xylium

import (
	"strconv" // Untuk mengonversi Content-Length int ke string.
	"strings" // Untuk manipulasi string (parsing tipe konten).

	"github.com/valyala/fasthttp" // Digunakan secara internal untuk operasi kompresi dan konstanta HTTP.
)

// GzipConfig mendefinisikan opsi konfigurasi untuk middleware Gzip.
// Middleware ini mengompresi body respons HTTP menggunakan Gzip untuk
// mengurangi ukuran transfer, yang dapat meningkatkan kinerja untuk klien
// dengan bandwidth terbatas atau untuk respons besar.
type GzipConfig struct {
	// Level adalah tingkat kompresi Gzip yang akan digunakan.
	// Tingkat yang lebih tinggi menawarkan rasio kompresi yang lebih baik tetapi mengonsumsi lebih banyak CPU.
	// Gunakan konstanta xylium.Compress* (misalnya, xylium.CompressDefaultCompression).
	//
	// Default: xylium.CompressDefaultCompression jika tidak ditentukan, atau jika diatur ke
	// xylium.CompressNoCompression (nilai 0). Jika Anda benar-benar ingin menonaktifkan
	// kompresi, jangan gunakan middleware Gzip sama sekali atau gunakan logika Skip.
	Level CompressionLevel

	// MinLength adalah panjang body respons minimum (dalam byte) yang diperlukan
	// untuk memicu kompresi. Respons yang lebih kecil dari ini tidak akan dikompresi,
	// karena overhead kompresi mungkin lebih besar daripada manfaatnya untuk payload kecil.
	//
	// Default: 0 (kompres semua respons yang memenuhi syarat terlepas dari ukurannya, jika tidak ditentukan).
	// Nilai praktis yang umum adalah 1024 byte (1KB).
	MinLength int

	// ContentTypes adalah daftar tipe MIME yang harus dipertimbangkan untuk kompresi.
	// Jika daftar ini kosong, daftar default tipe yang umum dapat dikompresi akan digunakan
	// (lihat `defaultCompressContentTypes`).
	// Perbandingan tidak case-sensitive dan mempertimbangkan tipe MIME dasar
	// (misalnya, "text/html" dari "text/html; charset=utf-8").
	//
	// Contoh: `[]string{"application/json", "text/html; charset=utf-8"}`
	ContentTypes []string
}

// defaultCompressContentTypes adalah daftar tipe MIME umum yang biasanya
// merupakan kandidat baik untuk kompresi Gzip. Format berbasis teks seperti HTML, CSS, JS, JSON,
// dan XML mendapat manfaat signifikan dari kompresi.
var defaultCompressContentTypes = []string{
	"text/plain", "text/html", "text/css", "text/xml", "text/javascript",
	"application/json", "application/xml", "application/javascript", "application/x-javascript",
	"application/rss+xml", "application/atom+xml", "image/svg+xml",
}

// Gzip mengembalikan middleware kompresi Gzip dengan konfigurasi default.
// Untuk kustomisasi, gunakan GzipWithConfig.
//
// Middleware ini akan:
//  1. Memeriksa header "Accept-Encoding" klien untuk dukungan "gzip".
//  2. Memeriksa apakah respons memenuhi syarat untuk kompresi (tipe konten, ukuran).
//  3. Mengompres body respons.
//  4. Menyetel header "Content-Encoding: gzip" dan "Vary: Accept-Encoding".
//  5. Memperbarui header "Content-Length" dengan ukuran body yang terkompresi.
func Gzip() Middleware {
	return GzipWithConfig(GzipConfig{}) // Melewatkan struct kosong untuk menggunakan default di GzipWithConfig
}

// GzipWithConfig mengembalikan middleware kompresi Gzip dengan konfigurasi kustom yang disediakan.
// Lihat GzipConfig untuk detail opsi yang tersedia.
func GzipWithConfig(config GzipConfig) Middleware {
	// Terapkan level kompresi default jika tidak ditentukan atau jika disetel ke NoCompression (0).
	// Jika pengguna benar-benar tidak ingin kompresi, mereka seharusnya tidak menggunakan middleware ini.
	if config.Level == CompressNoCompression {
		config.Level = CompressDefaultCompression
	}
	// Catatan: MinLength default ke 0 (kompres semua ukuran) jika tidak diset, yang dapat diterima.

	// Siapkan peta tipe konten yang dapat dikompresi untuk pencarian cepat.
	compressibleTypes := make(map[string]struct{})
	typesToUse := config.ContentTypes
	if len(typesToUse) == 0 {
		typesToUse = defaultCompressContentTypes // Gunakan default jika tidak ada yang disediakan.
	}
	for _, t := range typesToUse {
		// Normalisasi tipe: lowercase, hapus spasi, dan ambil hanya bagian utama (sebelum ';').
		normalizedType := strings.ToLower(strings.TrimSpace(strings.Split(t, ";")[0]))
		if normalizedType != "" {
			compressibleTypes[normalizedType] = struct{}{}
		}
	}

	// Fungsi middleware yang sebenarnya.
	return func(next HandlerFunc) HandlerFunc {
		return func(c *Context) error {
			// Dapatkan logger yang sudah request-scoped dari context Xylium.
			// Middleware ini menambahkan field "middleware": "Gzip" untuk konteks logging tambahan.
			logger := c.Logger().WithFields(M{"middleware": "Gzip"})

			// 1. Periksa apakah klien mendukung encoding gzip.
			acceptEncoding := c.Header("Accept-Encoding")
			if !strings.Contains(acceptEncoding, "gzip") {
				logger.Debugf("Klien tidak menerima encoding gzip ('%s'). Melewati kompresi untuk %s %s.",
					acceptEncoding, c.Method(), c.Path())
				return next(c) // Lanjutkan ke handler berikutnya tanpa kompresi.
			}

			// 2. Panggil handler berikutnya dalam chain untuk menyiapkan respons.
			err := next(c)
			if err != nil {
				// Jika ada error dari handler/middleware berikutnya, jangan lakukan kompresi.
				// Biarkan GlobalErrorHandler yang menangani error ini.
				logger.Debugf("Error terjadi dalam chain handler. Melewati kompresi untuk %s %s.", c.Method(), c.Path())
				return err
			}

			// 3. Periksa kondisi untuk melewati kompresi setelah handler dijalankan.
			// Kode status respons: Jangan kompres error (>=400).
			if c.Ctx.Response.StatusCode() >= StatusBadRequest {
				logger.Debugf("Kode status respons %d adalah >= 400. Melewati kompresi untuk %s %s.",
					c.Ctx.Response.StatusCode(), c.Method(), c.Path())
				return nil // Tidak ada error dari middleware Gzip, biarkan respons error asli dikirim.
			}
			// Content-Encoding sudah disetel: Mungkin sudah dikompresi oleh handler lain.
			if len(c.Ctx.Response.Header.Peek("Content-Encoding")) > 0 {
				logger.Debugf("Header 'Content-Encoding' sudah disetel ke '%s'. Melewati kompresi untuk %s %s.",
					string(c.Ctx.Response.Header.Peek("Content-Encoding")), c.Method(), c.Path())
				return nil
			}

			// Ambil body respons yang telah disiapkan oleh handler.
			responseBody := c.Ctx.Response.Body()
			// Body kosong: Tidak ada yang perlu dikompresi.
			if len(responseBody) == 0 {
				logger.Debugf("Body respons kosong. Melewati kompresi untuk %s %s.", c.Method(), c.Path())
				return nil
			}

			// Ukuran minimum: Jika body lebih kecil dari MinLength yang dikonfigurasi.
			if config.MinLength > 0 && len(responseBody) < config.MinLength {
				logger.Debugf("Panjang body respons %d byte kurang dari MinLength %d byte. Melewati kompresi untuk %s %s.",
					len(responseBody), config.MinLength, c.Method(), c.Path())
				return nil
			}

			// Tipe konten: Periksa apakah tipe konten respons ada dalam daftar yang dapat dikompresi.
			contentType := string(c.Ctx.Response.Header.ContentType())
			normalizedContentType := strings.ToLower(strings.Split(contentType, ";")[0])
			if _, typeIsCompressible := compressibleTypes[normalizedContentType]; !typeIsCompressible {
				logger.Debugf("Content-Type '%s' (dinormalisasi: '%s') tidak ada dalam daftar tipe yang dapat dikompresi. Melewati kompresi untuk %s %s.",
					contentType, normalizedContentType, c.Method(), c.Path())
				return nil
			}

			// 4. Lakukan kompresi Gzip.
			logger.Debugf("Mengompresi respons untuk %s %s (Content-Type: %s, Ukuran Asli: %d byte, Level: %d).",
				c.Method(), c.Path(), contentType, len(responseBody), config.Level)

			// Gunakan AppendGzipBytesLevel dari fasthttp. Cast config.Level ke int.
			compressedBody := fasthttp.AppendGzipBytesLevel(nil, responseBody, int(config.Level))

			// 5. Setel body dan header respons yang baru.
			c.Ctx.Response.SetBodyRaw(compressedBody)                        // Setel body yang sudah dikompresi.
			c.SetHeader("Content-Encoding", "gzip")                          // Tambahkan header Content-Encoding.
			c.SetHeader("Content-Length", strconv.Itoa(len(compressedBody))) // Update Content-Length.
			c.Ctx.Response.Header.Add("Vary", "Accept-Encoding")             // Tambahkan header Vary. Penting untuk caching.

			logger.Debugf("Kompresi berhasil untuk %s %s. Ukuran baru: %d byte.",
				c.Method(), c.Path(), len(compressedBody))

			return nil // Sukses, tidak ada error dari middleware Gzip itu sendiri.
		}
	}
}
