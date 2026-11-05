package sites

import (
	"encoding/json"
	"strconv"
)

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// SettingInt reads an integer policy setting (admin policy page), e.g.
// "site_max_mb". The environment (BUNKER_SITE_MAX_MB) still wins.
func SettingInt(key string, def int) int {
	if n, err := strconv.Atoi(app.Setting(key)); err == nil {
		return n
	}
	return def
}
