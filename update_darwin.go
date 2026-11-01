package main

import "github.com/tonyflux/pkp/internal/update"

func holdUpdateInstallLock() (func(), error) { return update.HoldInstallLock() }
