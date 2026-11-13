package config

// Exposed to the external tests of this package, which use configtest and so
// cannot live in package config without an import cycle.
var (
	SessionPath = func(s Store, name string) string { return s.sessionPath(name) }
	ConfigPath  = func(s Store) string { return s.configPath() }
)
