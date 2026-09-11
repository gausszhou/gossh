package utils

import (
	"os"
	"os/user"
	"strings"
)

// CurrentUser returns the name of the OS user running the process, used
// where a local identity is displayed (the built-in local server record
// and local-shell window titles). It falls back to the USER/USERNAME
// environment variables and finally to "local": none of the callers may
// fail because the lookup is unavailable (e.g. a stripped container).
func CurrentUser() string {
	if u, err := user.Current(); err == nil {
		if name := strings.TrimSpace(u.Username); name != "" {
			// Windows 域账号形如 DOMAIN\user,只保留账号名与主机行展示一致
			if i := strings.LastIndexAny(name, `\/`); i >= 0 && i < len(name)-1 {
				return name[i+1:]
			}
			return name
		}
	}
	for _, key := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return "local"
}
