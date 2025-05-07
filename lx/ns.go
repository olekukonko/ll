package lx

import (
	"strings"
	"sync"
)

// Namespace manages namespace enable/disable states with a cache.
// It provides thread-safe storage and retrieval of namespace states (enabled or disabled)
// for the ll package’s logging system. The struct uses two sync.Maps: one for persistent
// state (store) and one for cached computed states (cache), optimizing performance by
// reducing repeated hierarchy checks. Namespaces are hierarchical paths (e.g., "parent/child").
// Example usage in ll package:
//
//	logger := ll.New("parent").Enable()
//	logger.NamespaceDisable("parent/child") // Calls Namespace.Set
//	if !logger.NamespaceEnabled("parent/child") { // Calls Namespace.Get
//	    logger.Info("Namespace disabled") // Skipped due to disabled namespace
//	}
type Namespace struct {
	store sync.Map // Stores path -> bool (enabled/disabled) for explicit namespace states
	cache sync.Map // Stores path -> bool (cached enabled state) for computed hierarchy states
}

// Load retrieves a value from the store sync.Map.
// It returns the value and a boolean indicating whether the key exists. Used internally
// by Get and ll.Logger.NamespaceEnabled to check namespace states. Thread-safe via sync.Map.
// Example (internal usage):
//
//	if val, ok := ns.Load("parent/child"); ok {
//	    enabled := val.(bool) // true or false
//	}
func (ns *Namespace) Load(key any) (value any, ok bool) {
	return ns.store.Load(key)
}

// Store sets a value in the store sync.Map.
// It stores the key-value pair, overwriting any existing value. Used internally by Set
// to update namespace states. Thread-safe via sync.Map.
// Example (internal usage):
//
//	ns.Store("parent/child", true) // Enable namespace
func (ns *Namespace) Store(key, value any) {
	ns.store.Store(key, value)
}

// Set sets the enable/disable state for a namespace and invalidates the cache.
// It stores the enabled/disabled state for the specified namespace path and clears the cache
// for the path and its descendants to ensure updated hierarchy states. Thread-safe via sync.Map.
// Example (via ll package):
//
//	logger := ll.New("parent").NamespaceDisable("parent/child") // Calls Set("parent/child", false)
//	logger.Namespace("child").Info("Ignored") // No output due to disabled namespace
func (ns *Namespace) Set(path string, enabled bool) {
	// Store the enable/disable state
	ns.store.Store(path, enabled)
	// Invalidate cache for the path
	ns.cache.Delete(path)
	// Invalidate cache for all descendant paths
	ns.cache.Range(func(key, _ interface{}) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, path+Slash) {
			ns.cache.Delete(k)
		}
		return true
	})
	// Note: Commented-out debug print removed for production; consider logging via ll.Logger if needed
	// fmt.Printf("Namespace %s: storing %s=%v\n", path, path, enabled)
}

// Get retrieves the enable/disable state for a namespace.
// It returns the state (true for enabled, false for disabled) and a boolean indicating whether
// the namespace has an explicit state in the store. If no state is found, it defaults to
// enabled (true, false). Thread-safe via sync.Map.
// Example (via ll package):
//
//	logger := ll.New("parent")
//	enabled, exists := logger.namespaces.Get("parent/child") // Returns true, false (default enabled)
//	logger.NamespaceDisable("parent/child")
//	enabled, exists = logger.namespaces.Get("parent/child") // Returns false, true
func (ns *Namespace) Get(path string) (bool, bool) {
	if val, ok := ns.store.Load(path); ok {
		return val.(bool), true
	}
	return true, false // Default enabled
}
