package lx

import (
	"strings"
	"sync"
	"sync/atomic"
)

// namespaceRule stores the cached result of Enabled.
type namespaceRule struct {
	isEnabledByRule  bool
	isDisabledByRule bool
	generation       uint64 // NEW: track cache validity
}

// Namespace manages thread-safe namespace enable/disable states with caching.
// The store holds explicit user-defined rules (path -> bool).
// The cache holds computed effective states for paths (path -> namespaceRule)
// based on hierarchical rules to optimize lookups.
type Namespace struct {
	store      sync.Map      // path (string) -> rule (bool)
	cache      sync.Map      // path (string) -> namespaceRule
	levels     sync.Map      // path (string) -> LevelType (per-namespace threshold)
	genCounter atomic.Uint64 // NEW: atomic generation counter
	hasRules   atomic.Bool   // true once any explicit rule exists (fast-path guard)
	hasLevels  atomic.Bool   // true once any per-namespace level is configured
}

// Set defines an explicit enable/disable rule for a namespace path.
// It clears the cache to ensure subsequent lookups reflect the change.
func (ns *Namespace) Set(path string, enabled bool) {
	ns.store.Store(path, enabled)
	ns.hasRules.Store(true)
	ns.invalidatePathCache(path)
}

// HasRules reports whether any explicit namespace rule has ever been registered.
// It is a lock-free guard used by the logging fast path: when no rules exist,
// Enabled always returns (false, false) and the sync.Map lookups can be skipped
// entirely. This is the common case (most applications never call
// NamespaceEnable/NamespaceDisable).
func (ns *Namespace) HasRules() bool {
	return ns != nil && ns.hasRules.Load()
}

// SetLevel configures a minimum level for a namespace path. Descendants inherit
// the most specific configured level; entries below it are dropped even when the
// logger itself would allow them.
func (ns *Namespace) SetLevel(path string, level LevelType) {
	ns.levels.Store(path, level)
	ns.hasLevels.Store(true)
}

// HasLevels reports whether any per-namespace level has been configured. It is
// the lock-free guard for the level lookup in the logging hot path.
func (ns *Namespace) HasLevels() bool {
	return ns != nil && ns.hasLevels.Load()
}

// Level returns the most specific configured level for path (walking up the
// hierarchy) and whether one was found. It is allocation free.
func (ns *Namespace) Level(path, separator string) (LevelType, bool) {
	if path == "" || !ns.HasLevels() {
		return LevelNone, false
	}
	if separator == "" {
		separator = Slash
	}
	for prefix := path; ; {
		if val, ok := ns.levels.Load(prefix); ok {
			if level, ok := val.(LevelType); ok {
				return level, true
			}
			return LevelNone, false
		}
		idx := strings.LastIndex(prefix, separator)
		if idx < 0 {
			return LevelNone, false
		}
		prefix = prefix[:idx]
	}
}

// CacheLen returns the current number of cached computed entries. Intended for
// diagnostics and tests.
func (ns *Namespace) CacheLen() int {
	n := 0
	ns.cache.Range(func(_, _ interface{}) bool {
		n++
		return true
	})
	return n
}

// invalidatePathCache increments generation counter instead of scanning cache.
func (ns *Namespace) invalidatePathCache(path string) {
	// Atomic increment - O(1), no lock contention on cache
	ns.genCounter.Add(1)
}

// Store directly sets a rule in the store, bypassing cache invalidation.
// Intended for internal use or sync.Map parity; prefer Set for standard use.
func (ns *Namespace) Store(path string, rule bool) {
	ns.store.Store(path, rule)
	ns.hasRules.Store(true)
}

// clearCache clears the cache of Enabled results.
// Called by Set to ensure consistency after rule changes.
func (ns *Namespace) clearCache() {
	ns.cache.Range(func(key, _ interface{}) bool {
		ns.cache.Delete(key)
		return true
	})
}

// Enabled checks if a path is enabled by namespace rules, considering the most
// specific rule (path or closest prefix) in the store. Results are cached.
// Args:
//   - path: Absolute namespace path to check.
//   - separator: Character delimiting path segments (e.g., "/", ".").
//
// Returns:
//   - isEnabledByRule: True if an explicit rule enables the path.
//   - isDisabledByRule: True if an explicit rule disables the path.
//
// If both are false, no explicit rule applies to the path or its prefixes.
//
// The prefix walk is allocation free: it sub-slices the path instead of using
// strings.Split/strings.Join, which used to allocate on every cache miss.
func (ns *Namespace) Enabled(path string, separator string) (isEnabledByRule bool, isDisabledByRule bool) {
	if path == "" {
		return false, false
	}

	// Check cache with generation validation
	if cachedValue, found := ns.cache.Load(path); found {
		if state, ok := cachedValue.(namespaceRule); ok {
			// If cache generation matches current, result is valid
			if state.generation == ns.genCounter.Load() {
				return state.isEnabledByRule, state.isDisabledByRule
			}
			// Stale cache - fall through to recompute
			ns.cache.Delete(path)
		}
	}
	if separator == "" {
		separator = Slash
	}

	// Compute: Most specific rule wins (original logic)
	computedIsEnabled := false
	computedIsDisabled := false
	for prefix := path; ; {
		if val, ok := ns.store.Load(prefix); ok {
			if rule, ok := val.(bool); ok {
				if rule {
					computedIsEnabled = true
					computedIsDisabled = false
				} else {
					computedIsEnabled = false
					computedIsDisabled = true
				}
				break
			}
		}
		idx := strings.LastIndex(prefix, separator)
		if idx < 0 {
			break
		}
		prefix = prefix[:idx]
	}

	// Cache result with current generation
	ns.cache.Store(path, namespaceRule{
		isEnabledByRule:  computedIsEnabled,
		isDisabledByRule: computedIsDisabled,
		generation:       ns.genCounter.Load(),
	})
	return computedIsEnabled, computedIsDisabled
}
