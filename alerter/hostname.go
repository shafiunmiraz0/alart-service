package alerter

import "os"

func getHostnameOS() (string, error) {
	return os.Hostname()
}
