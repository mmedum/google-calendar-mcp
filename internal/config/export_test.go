package config

// Exported for tests in the config_test package.

// Defined exposes defined: every variable Define reads, in order.
func Defined() []Var { return defined() }
