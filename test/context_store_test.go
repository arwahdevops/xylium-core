package xylium_test

import (
	"fmt"
	"sync" // Untuk menguji mu di Context jika diperlukan, meskipun tidak langsung di sini
	"testing"

	// Ganti path ini sesuai dengan module path Anda
	"github.com/arwahdevops/xylium-core/src/xylium"
	// "github.com/stretchr/testify/assert" // Opsional
)

// Helper untuk membuat xylium.Context standar untuk pengujian store.
// Kita bisa menggunakan NewContextForTest karena store adalah bagian dari Context dasar.
func newTestContextForStore() *xylium.Context {
	// Params dan fasthttpCtx bisa nil jika tidak relevan untuk tes store murni
	return xylium.NewContextForTest(nil, nil)
}

func TestContext_Store_Set_Get(t *testing.T) {
	ctx := newTestContextForStore()

	key1 := "myString"
	val1 := "hello world"
	key2 := "myInt"
	val2 := 123
	key3 := "myBool"
	val3 := true
	key4 := "myStruct"
	val4 := struct{ Name string }{Name: "Xylium"}

	// Test Set
	ctx.Set(key1, val1)
	ctx.Set(key2, val2)
	ctx.Set(key3, val3)
	ctx.Set(key4, val4)

	// Test Get - Key Exists
	retVal1, exists1 := ctx.Get(key1)
	if !exists1 {
		t.Errorf("Get(%s): expected key to exist", key1)
	}
	if retVal1 != val1 {
		t.Errorf("Get(%s): expected value '%v', got '%v'", key1, val1, retVal1)
	}

	retVal2, exists2 := ctx.Get(key2)
	if !exists2 {
		t.Errorf("Get(%s): expected key to exist", key2)
	}
	if retVal2 != val2 {
		t.Errorf("Get(%s): expected value '%v', got '%v'", key2, val2, retVal2)
	}

	retVal3, exists3 := ctx.Get(key3)
	if !exists3 {
		t.Errorf("Get(%s): expected key to exist", key3)
	}
	if retVal3 != val3 {
		t.Errorf("Get(%s): expected value '%v', got '%v'", key3, val3, retVal3)
	}

	retVal4, exists4 := ctx.Get(key4)
	if !exists4 {
		t.Errorf("Get(%s): expected key to exist", key4)
	}
	if retVal4 != val4 { // Perbandingan struct akan membandingkan field
		t.Errorf("Get(%s): expected value '%v', got '%v'", key4, val4, retVal4)
	}

	// Test Get - Key Not Exists
	nonExistentKey := "nonExistent"
	retValNon, existsNon := ctx.Get(nonExistentKey)
	if existsNon {
		t.Errorf("Get(%s): expected key to not exist, but it did", nonExistentKey)
	}
	if retValNon != nil {
		t.Errorf("Get(%s): expected nil value for non-existent key, got '%v'", nonExistentKey, retValNon)
	}

	// Test Set - Override Value
	newVal1 := "new hello"
	ctx.Set(key1, newVal1)
	retVal1Overridden, _ := ctx.Get(key1)
	if retVal1Overridden != newVal1 {
		t.Errorf("Get(%s) after override: expected value '%v', got '%v'", key1, newVal1, retVal1Overridden)
	}
}

func TestContext_Store_MustGet(t *testing.T) {
	ctx := newTestContextForStore()
	key := "mustGetKey"
	val := "mustGetValue"
	ctx.Set(key, val)

	// Test MustGet - Key Exists
	retVal := ctx.MustGet(key)
	if retVal != val {
		t.Errorf("MustGet(%s): expected value '%v', got '%v'", key, val, retVal)
	}

	// Test MustGet - Key Not Exists (should panic)
	nonExistentKey := "nonExistentMustGet"
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("MustGet(%s) did not panic when key does not exist", nonExistentKey)
		} else {
			// Opsional: periksa pesan panic
			expectedPanicMsg := "xylium: key '" + nonExistentKey + "' does not exist in context store"
			if r.(string) != expectedPanicMsg { // Asumsikan panic dengan string
				t.Errorf("MustGet(%s) panic message: expected '%s', got '%s'", nonExistentKey, expectedPanicMsg, r)
			}
		}
	}()
	_ = ctx.MustGet(nonExistentKey) // Ini harus memicu panic
}

func TestContext_Store_TypedGetters(t *testing.T) {
	ctx := newTestContextForStore()

	strKey, strVal := "myString", "xylium"
	intKey, intVal := "myInt", 42
	boolKey, boolVal := "myBool", true
	wrongTypeKey := "wrongType"

	ctx.Set(strKey, strVal)
	ctx.Set(intKey, intVal)
	ctx.Set(boolKey, boolVal)
	ctx.Set(wrongTypeKey, 123) // Simpan int

	// GetString
	s, exists := ctx.GetString(strKey)
	if !exists || s != strVal {
		t.Errorf("GetString(%s): expected '%s' and true, got '%s' and %t", strKey, strVal, s, exists)
	}
	sNon, existsNon := ctx.GetString("nonExistentStr")
	if existsNon || sNon != "" {
		t.Errorf("GetString(nonExistentStr): expected '' and false, got '%s' and %t", sNon, existsNon)
	}
	sWrong, existsWrong := ctx.GetString(intKey) // Mencoba mendapatkan int sebagai string
	if existsWrong || sWrong != "" {
		t.Errorf("GetString(%s) with wrong type: expected '' and false, got '%s' and %t", intKey, sWrong, existsWrong)
	}

	// GetInt
	i, exists := ctx.GetInt(intKey)
	if !exists || i != intVal {
		t.Errorf("GetInt(%s): expected %d and true, got %d and %t", intKey, intVal, i, exists)
	}
	iNon, existsNon := ctx.GetInt("nonExistentInt")
	if existsNon || iNon != 0 {
		t.Errorf("GetInt(nonExistentInt): expected 0 and false, got %d and %t", iNon, existsNon)
	}
	iWrong, existsWrong := ctx.GetInt(strKey) // Mencoba mendapatkan string sebagai int
	if existsWrong || iWrong != 0 {
		t.Errorf("GetInt(%s) with wrong type: expected 0 and false, got %d and %t", strKey, iWrong, existsWrong)
	}

	// GetBool
	b, exists := ctx.GetBool(boolKey)
	if !exists || b != boolVal {
		t.Errorf("GetBool(%s): expected %t and true, got %t and %t", boolKey, boolVal, b, exists)
	}
	bNon, existsNon := ctx.GetBool("nonExistentBool")
	if existsNon || bNon != false {
		t.Errorf("GetBool(nonExistentBool): expected false and false, got %t and %t", bNon, existsNon)
	}
	bWrong, existsWrong := ctx.GetBool(strKey) // Mencoba mendapatkan string sebagai bool
	if existsWrong || bWrong != false {
		t.Errorf("GetBool(%s) with wrong type: expected false and false, got %t and %t", strKey, bWrong, existsWrong)
	}
}

// Opsional: Tes untuk konkurensi jika c.mu di context.go adalah bagian dari tes ini.
// Namun, karena store internal konteks biasanya tidak diakses secara konkuren oleh
// goroutine pengguna dalam satu request, tes konkurensi lebih relevan untuk
// komponen yang memang dirancang untuk itu (seperti logger global atau rate limiter store).
// Jika ingin menguji c.mu secara spesifik (meskipun ini lebih white-box):
func TestContext_Store_Concurrency(t *testing.T) {
	// Tes ini lebih untuk memastikan c.mu (RWMutex) bekerja,
	// meskipun aplikasi biasanya tidak akan memanggil Set/Get pada konteks yang sama
	// dari goroutine yang berbeda secara bersamaan.
	ctx := newTestContextForStore()
	var wg sync.WaitGroup
	numGoroutines := 100
	numOpsPerGoroutine := 100

	// Operasi Set konkuren
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gIndex int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", gIndex, j)
				val := gIndex*1000 + j
				ctx.Set(key, val)
			}
		}(i)
	}
	wg.Wait()

	// Operasi Get konkuren (setelah semua Set selesai)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(gIndex int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", gIndex, j)
				expectedVal := gIndex*1000 + j
				val, exists := ctx.Get(key)
				if !exists {
					t.Errorf("Concurrent Get: key %s not found", key)
					continue
				}
				if val.(int) != expectedVal { // Asumsikan semua nilai adalah int
					t.Errorf("Concurrent Get: key %s expected %d, got %v", key, expectedVal, val)
				}
			}
		}(i)
	}
	wg.Wait()
}
