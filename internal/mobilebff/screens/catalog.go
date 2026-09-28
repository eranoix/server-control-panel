package screens

import "server-control-panel/internal/mobilebff/sdui"

func alwaysVisible(sdui.Viewer) bool { return true }

func adminOnly(v sdui.Viewer) bool { return v.IsAdmin() }
