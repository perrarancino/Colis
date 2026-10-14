package main

import "golang.org/x/sys/windows/registry"

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutostart adds or removes the "start with Windows" entry for the current user.
func SetAutostart(exe string, on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue("EasyGit")
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	return k.SetStringValue("EasyGit", `"`+exe+`" --minimized`)
}
