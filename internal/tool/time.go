package tool

import "time"

// TimeNow 返回当前时间 RFC3339（只读，无参数）。
func TimeNow() string {
	return time.Now().Format(time.RFC3339)
}
