//go:build !windows && !darwin

package main

func holdUpdateInstallLock() (func(), error) { return func() {}, nil }
