package ratelimit

import "time"

// event là một lần gọi model đã ghi nhận: thời điểm + token thực (hoặc ước lượng chờ Record).
type event struct {
	TS     int64 `json:"ts"`     // unix nano
	Tokens int   `json:"tokens"` // token của request này (est trước, thực sau Record)
}

// Limits là giới hạn hiệu lực cho một (provider,model). 0 = không giới hạn chiều đó.
type Limits struct {
	RPM           int `json:"rpm,omitempty"`
	RPD           int `json:"rpd,omitempty"`
	TPM           int `json:"tpm,omitempty"`
	MaxConcurrent int `json:"max_concurrent,omitempty"`
}

const (
	minuteWindow = time.Minute
	dayWindow    = 24 * time.Hour
)

// prune bỏ mọi event cũ hơn 24h (cửa sổ lớn nhất). Giữ log bounded.
func prune(evs []event, now time.Time) []event {
	cutoff := now.Add(-dayWindow).UnixNano()
	i := 0
	for i < len(evs) && evs[i].TS < cutoff {
		i++
	}
	if i == 0 {
		return evs
	}
	return append(evs[:0], evs[i:]...)
}

// check tính xem thêm 1 request (estTokens) có vượt bất kỳ giới hạn nào không.
// Trả về retryAfter=0 nếu OK; ngược lại là khoảng chờ tối thiểu tới khi có slot.
// evs PHẢI đã prune + sort tăng theo TS.
func check(evs []event, lim Limits, now time.Time, estTokens int) time.Duration {
	var worst time.Duration

	// RPD: đếm event trong 24h cuộn.
	if lim.RPD > 0 {
		dayStart := now.Add(-dayWindow).UnixNano()
		cnt := 0
		var oldest int64
		for _, e := range evs {
			if e.TS >= dayStart {
				if cnt == 0 {
					oldest = e.TS
				}
				cnt++
			}
		}
		if cnt >= lim.RPD {
			// slot mở khi event cũ nhất trong cửa sổ rời khỏi 24h.
			ra := time.Duration(oldest-dayStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	// RPM: đếm event trong 60s.
	if lim.RPM > 0 {
		minStart := now.Add(-minuteWindow).UnixNano()
		cnt := 0
		var oldest int64
		for _, e := range evs {
			if e.TS >= minStart {
				if cnt == 0 {
					oldest = e.TS
				}
				cnt++
			}
		}
		if cnt >= lim.RPM {
			ra := time.Duration(oldest-minStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	// TPM: tổng token trong 60s + estTokens.
	if lim.TPM > 0 {
		minStart := now.Add(-minuteWindow).UnixNano()
		sum := 0
		var oldest int64
		first := true
		for _, e := range evs {
			if e.TS >= minStart {
				if first {
					oldest = e.TS
					first = false
				}
				sum += e.Tokens
			}
		}
		if sum+estTokens > lim.TPM && !first {
			ra := time.Duration(oldest-minStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	return worst
}
