package selfupdate

import "errors"

func sysExec(string, []string, []string) error {
	return errors.New("not supported on Windows")
}
