//go:build !darwin && !windows

package config

func normalizeSecretPathCase(path string) string { return path }
