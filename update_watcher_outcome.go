package main

func windowsUpdateOutcome(exit uint32) (string, bool) {
	switch exit {
	case 0:
		return "", true
	case 71:
		return "installBusy", true
	case 72:
		return "installRollback", false
	case 73:
		return "installLocation", true
	default:
		return "install", true
	}
}
