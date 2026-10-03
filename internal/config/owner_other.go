//go:build !unix

package config

// privateToMe: without unix ownership there is no system config to move.
var privateToMe = func(path string) bool { return false }
