package config

import "sync"

// reset clears the once so tests can reload with a different NABD_CONFIG.
func reset() {
	once = sync.Once{}
	values = nil
	loadErr = nil
}
