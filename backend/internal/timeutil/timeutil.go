// Package timeutil 集中处理时区。
//
// 全系统统一 GMT+8（Asia/Shanghai）。KB 教训 [Go][TZ]：日界/倒计时/到期判定若走 UTC 的
// Year/Month/Day，00:00–07:59 会整体错一天。因此业务侧一律通过本包取时间，禁止直接
// time.Now().UTC() 参与民用日期计算。
package timeutil

import "time"

// Shanghai 是全系统唯一的业务时区。
var Shanghai = mustLoadShanghai()

func mustLoadShanghai() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	// 容器缺失 tzdata 时的兜底：固定 +08:00 偏移。中国大陆无夏令时，语义等价。
	return time.FixedZone("CST", 8*60*60)
}

// Now 返回北京时间的当前时刻。
func Now() time.Time { return time.Now().In(Shanghai) }

// In 把任意时刻转换到北京时区。
func In(t time.Time) time.Time { return t.In(Shanghai) }

// StartOfDay 返回该时刻所在民用日的 00:00:00（北京时区）。
func StartOfDay(t time.Time) time.Time {
	l := t.In(Shanghai)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, Shanghai)
}

// Until 返回距目标时刻的剩余时长，已过期则返回 0。
func Until(target time.Time) time.Duration {
	d := target.Sub(Now())
	if d < 0 {
		return 0
	}
	return d
}

// FormatCN 以人类可读的中文习惯格式化（用于系统消息文案）。
func FormatCN(t time.Time) string {
	return t.In(Shanghai).Format("2006-01-02 15:04")
}
