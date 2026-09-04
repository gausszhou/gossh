//go:build linux && !cgo

package desktop

import (
	"fmt"
)

// RunTray 在 CGO_ENABLED=0 构建下不可用:托盘依赖 getlantern/systray
// (GTK/AppIndicator,cgo)。给出明确报错而不是静默降级。
func RunTray(o TrayOptions) error {
	return fmt.Errorf("desktop mode requires a cgo build (CGO_ENABLED=1): the tray needs GTK/AppIndicator; rebuild with `CGO_ENABLED=1 go build` or use `gossh serve`")
}
