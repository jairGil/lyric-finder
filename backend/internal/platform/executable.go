package platform

import "runtime"

func ExecutableName(name string) string {
	return executableNameForOS(name, runtime.GOOS)
}

func executableNameForOS(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
