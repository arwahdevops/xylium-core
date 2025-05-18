package xylium

import "fmt" // For fmt.Sprintf in MustGet panic message.

// --- Context State Management (Store) ---
// The Context store provides a way to pass data between middleware and handlers
// within the scope of a single HTTP request. It's a key-value map protected
// by a RWMutex for concurrent access safety (though typical request handling
// is single-goroutine, mutex guards against potential misuse or future patterns).

// Set stores a key-value pair in the context's private store.
// This is useful for middleware (e.g., authentication middleware setting user details)
// to make data available to subsequent handlers in the chain.
// The store is protected by a RWMutex, making this operation thread-safe.
func (c *Context) Set(key string, value interface{}) {
	c.mu.Lock()         // Acquire write lock to modify the store.
	if c.store == nil { // Defensive initialization, though pool.New should handle this.
		c.store = make(map[string]interface{})
	}
	c.store[key] = value
	c.mu.Unlock() // Release write lock.
}

// Get retrieves a value from the context's store by its key.
// It returns the value (as `interface{}`) and a boolean `exists` which is true
// if the key was found in the store, and false otherwise.
// This operation is thread-safe due to RWMutex.
func (c *Context) Get(key string) (value interface{}, exists bool) {
	c.mu.RLock()        // Acquire read lock to access the store.
	if c.store != nil { // Check if store is initialized.
		value, exists = c.store[key]
	} else {
		// If store is nil (should not happen in normal lifecycle), key effectively doesn't exist.
		exists = false
	}
	c.mu.RUnlock() // Release read lock.
	return
}

// MustGet retrieves a value from the context's store by its key.
// Unlike `Get`, it panics if the key does not exist in the store.
// This should be used when the presence of the key is guaranteed by prior
// middleware or application logic.
// This operation is thread-safe.
func (c *Context) MustGet(key string) interface{} {
	val, ok := c.Get(key) // Internally uses RLock for thread-safety.
	if !ok {
		panic(fmt.Sprintf("xylium: key '%s' does not exist in context store", key))
	}
	return val
}

// GetString retrieves a value from the store and asserts it as a string.
// Returns the string value and true if the key exists and the value is indeed a string.
// Otherwise, it returns an empty string and false.
// This operation is thread-safe.
func (c *Context) GetString(key string) (s string, exists bool) {
	val, ok := c.Get(key) // Internally uses RLock.
	if !ok {
		return "", false // Key not found.
	}
	s, exists = val.(string) // Type assertion.
	return
}

// GetInt retrieves a value from the store and asserts it as an int.
// Returns the int value and true if the key exists and the value is indeed an int.
// Otherwise, it returns 0 and false.
// This operation is thread-safe.
func (c *Context) GetInt(key string) (i int, exists bool) {
	val, ok := c.Get(key) // Internally uses RLock.
	if !ok {
		return 0, false // Key not found.
	}
	i, exists = val.(int) // Type assertion.
	return
}

// GetBool retrieves a value from the store and asserts it as a bool.
// Returns the bool value and true if the key exists and the value is indeed a bool.
// Otherwise, it returns false and false. (Note: if key exists but value is not bool, exists is false from assertion)
// This operation is thread-safe.
func (c *Context) GetBool(key string) (b bool, exists bool) {
	val, ok := c.Get(key) // Internally uses RLock.
	if !ok {
		return false, false // Key not found.
	}
	b, exists = val.(bool) // Type assertion.
	return
}
