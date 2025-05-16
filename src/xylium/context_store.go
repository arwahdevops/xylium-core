package xylium

import "fmt"

// --- State Management (Context Store) ---

// Set stores a key-value pair in the context's store.
// This is useful for passing data between middleware and handlers.
// The store is protected by a RWMutex.
func (c *Context) Set(key string, value interface{}) {
	c.mu.Lock()
	// c.store is initialized in pool's New or context.reset
	c.store[key] = value
	c.mu.Unlock()
}

// Get retrieves a value from the context's store by key.
// It returns the value and a boolean indicating if the key exists.
func (c *Context) Get(key string) (value interface{}, exists bool) {
	c.mu.RLock()
	// c.store is initialized
	value, exists = c.store[key]
	c.mu.RUnlock()
	return
}

// MustGet retrieves a value from the context's store by key.
// It panics if the key does not exist.
func (c *Context) MustGet(key string) interface{} {
	val, ok := c.Get(key)
	if !ok {
		panic(fmt.Sprintf("xylium: key '%s' does not exist in context store", key))
	}
	return val
}

// GetString retrieves a value from the store as a string.
// Returns the string and true if the key exists and the value is a string.
// Otherwise, returns an empty string and false.
func (c *Context) GetString(key string) (s string, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return "", false
	}
	s, exists = val.(string)
	return
}

// GetInt retrieves a value from the store as an int.
// Returns the int and true if the key exists and the value is an int.
// Otherwise, returns 0 and false.
func (c *Context) GetInt(key string) (i int, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	i, exists = val.(int)
	return
}

// GetBool retrieves a value from the store as a bool.
// Returns the bool and true if the key exists and the value is a bool.
// Otherwise, returns false and false.
func (c *Context) GetBool(key string) (b bool, exists bool) {
	val, ok := c.Get(key)
	if !ok {
		return false, false
	}
	b, exists = val.(bool)
	return
}
