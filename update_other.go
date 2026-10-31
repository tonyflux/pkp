//go:build !windows

package main

func holdUpdateInstallLock() (func(), error) { return func() {}, nil }
