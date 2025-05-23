// src/xylium/compression.go

// Package xylium menyediakan fungsionalitas inti untuk framework web Xylium.
package xylium

import "github.com/valyala/fasthttp" // Digunakan secara internal untuk mengimplementasikan level kompresi.

// CompressionLevel mendefinisikan tipe untuk berbagai level kompresi Gzip
// yang dapat dikonfigurasi dalam middleware Gzip Xylium.
// Nilai-nilai ini adalah alias untuk konstanta yang relevan dari package fasthttp,
// menyediakan abstraksi agar pengguna framework tidak perlu mengimpor fasthttp secara langsung
// untuk konfigurasi dasar.
type CompressionLevel int

// Konstanta untuk level kompresi Gzip.
// Ini memungkinkan pengguna untuk menentukan trade-off antara kecepatan kompresi
// dan rasio kompresi yang dihasilkan.
const (
	// CompressNoCompression (0) menunjukkan bahwa tidak ada kompresi yang harus dilakukan.
	// Jika disetel dalam GzipConfig.Level, middleware Gzip akan menggunakan
	// CompressDefaultCompression sebagai gantinya, kecuali jika middleware Gzip secara keseluruhan
	// ingin dilewati.
	CompressNoCompression CompressionLevel = CompressionLevel(fasthttp.CompressNoCompression)

	// CompressBestSpeed (1) mengonfigurasi Gzip untuk kompresi tercepat,
	// yang mungkin menghasilkan rasio kompresi yang lebih rendah.
	CompressBestSpeed CompressionLevel = CompressionLevel(fasthttp.CompressBestSpeed)

	// CompressBestCompression (9) mengonfigurasi Gzip untuk rasio kompresi terbaik,
	// yang mungkin lebih lambat dan menggunakan lebih banyak CPU.
	CompressBestCompression CompressionLevel = CompressionLevel(fasthttp.CompressBestCompression)

	// CompressDefaultCompression (-1) menggunakan level kompresi default yang disediakan
	// oleh library kompresi yang mendasarinya (biasanya level 6 untuk Gzip).
	// Ini umumnya menawarkan keseimbangan yang baik antara kecepatan dan rasio kompresi.
	// Ini adalah level default yang digunakan oleh middleware Gzip Xylium jika tidak ada
	// level lain yang ditentukan atau jika CompressNoCompression ditentukan sebagai level.
	CompressDefaultCompression CompressionLevel = CompressionLevel(fasthttp.CompressDefaultCompression)

	// CompressHuffmanOnly (-2) adalah mode khusus yang hanya menggunakan pengkodean Huffman.
	// Ini lebih cepat daripada kompresi Gzip penuh tetapi dengan rasio kompresi yang lebih rendah.
	CompressHuffmanOnly CompressionLevel = CompressionLevel(fasthttp.CompressHuffmanOnly)
)
