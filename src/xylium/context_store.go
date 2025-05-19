package xylium

import "fmt" // For fmt.Sprintf in MustGet panic message.

// --- Context State Management (Store) ---
// The Context store provides a way to pass data between middleware and handlers
// within the scope of a single HTTP request. It is protected by a RWMutex.

// Set stores a key-value pair in the context's private store.
// This operation is thread-safe.
// It assumes c.mu and c.store are always valid (initialized by pool.New/acquireCtx).
func (c *Context) Set(key string, value interface{}) {
	// c.mu is a pointer, so Lock() is called on the RWMutex instance it points to.
	// c.store is a map, assumed to be initialized.
	c.mu.Lock()
	c.store[key] = value
	c.mu.Unlock()
}

// Get retrieves a value from the context's store by its key.
// Returns the value and true if the key exists, otherwise nil and false.
// This operation is thread-safe.
// It assumes c.mu and c.store are always valid.
func (c *Context) Get(key string) (value interface{}, exists bool) {
	c.mu.RLock()
	value, exists = c.store[key]
	c.mu.RUnlock()
	return
}

// MustGet retrieves a value from the context's store by its key.
// It panics if the key does not exist in the store.
// This operation is thread-safe as it uses c.Get().
func (c *Context) MustGet(key string) interface{} {
	val, ok := c.Get(key)
	if !ok {
		panic(fmt.Sprintf("xylium: key '%s' does not exist in context store", key))
	}
	return val
}

// GetString retrieves a value from the store and asserts it as a string.
// Returns the string value and true if the key exists and the value is a string.
// Otherwise, it returns an empty string and false.
// This operation is thread-safe.
func (c *Context) GetString(key string) (s string, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return "", false
	}
	s, exists = val.(string)
	return
}

// GetInt retrieves a value from the store and asserts it as an int.
// Returns the int value and true if the key exists and the value is an int.
// Otherwise, it returns 0 and false.
// This operation is thread-safe.
func (c *Context) GetInt(key string) (i int, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	i, exists = val.(int)
	return
}

// GetBool retrieves a value from the store and asserts it as a bool.
// Returns the bool value and true if the key exists and the value is a bool.
// Otherwise, it returns false and false.
// This operation is thread-safe.
func (c *Context) GetBool(key string) (b bool, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return false, false
	}
	b, exists = val.(bool)
	return
}
