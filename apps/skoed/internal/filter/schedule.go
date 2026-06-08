package filter

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/skoed/skoed/internal/config"
)

// ScheduleModeBlockInside is the default — the bound blocklist is active
// ONLY while the schedule's window is active (e.g. "block social Mon-Fri
// 20:00-23:59"). Outside the window the blocklist effectively becomes a no-op.
const ScheduleModeBlockInside = "block_only_inside"

// ScheduleModeAllowInside inverts the meaning — the bound blocklist is active
// EXCEPT while the schedule's window is active (e.g. "allow social only
// during homework hour"). Outside the window the blocklist is enforced.
const ScheduleModeAllowInside = "allow_only_inside"

// ScheduleResult reports the outcome of evaluating a (profile, blocklist)
// binding against the current wall-clock.
type ScheduleResult int

const (
	// ScheduleNoBinding: no schedule binds this (profile, blocklist) pair;
	// the blocklist applies as normal.
	ScheduleNoBinding ScheduleResult = iota
	// ScheduleApplies: the binding's schedule is active and tells the engine
	// to block. The blocklist's match should produce an NXDOMAIN.
	ScheduleApplies
	// ScheduleSuppresses: the binding's schedule is inactive (or actively
	// allowing); the blocklist is bypassed for this query.
	ScheduleSuppresses
)

// EvaluateSchedules walks the supplied bindings for the given (profile,
// blocklist) pair, evaluates each against `now`, and returns whether any
// binding tells the engine to apply / suppress the blocklist.
//
// Semantics when multiple bindings overlap: ANY active "applies" wins
// (most restrictive), then any active "suppresses", then NoBinding.
// Operators wanting deterministic per-pair semantics should keep at most
// one binding per pair; the API doesn't enforce uniqueness but the UI
// surfaces it.
func EvaluateSchedules(bindings []config.ScheduleBinding, schedules []config.Schedule, profileID, blocklistID string, now time.Time) ScheduleResult {
	var any bool
	var applies, suppresses bool
	for _, b := range bindings {
		if b.ProfileID != profileID || b.BlocklistID != blocklistID {
			continue
		}
		s := findSchedule(schedules, b.ScheduleID)
		if s == nil {
			continue
		}
		any = true
		active := scheduleActive(s, now)
		switch s.Mode {
		case ScheduleModeAllowInside:
			// "allow inside" means INSIDE the window, the blocklist is
			// suppressed. Outside the window, the blocklist applies.
			if active {
				suppresses = true
			} else {
				applies = true
			}
		default:
			// Block-inside (or any unknown mode falls back to this safe
			// default): INSIDE the window, the blocklist applies. Outside,
			// it's suppressed.
			if active {
				applies = true
			} else {
				suppresses = true
			}
		}
	}
	if !any {
		return ScheduleNoBinding
	}
	if applies {
		return ScheduleApplies
	}
	if suppresses {
		return ScheduleSuppresses
	}
	return ScheduleNoBinding
}

func findSchedule(schedules []config.Schedule, id string) *config.Schedule {
	for i := range schedules {
		if schedules[i].ID == id {
			return &schedules[i]
		}
	}
	return nil
}

// scheduleActive returns true if `now` falls inside any of the schedule's
// windows. Times in the schedule are interpreted as wall-clock times in
// the supplied time's Location.
func scheduleActive(s *config.Schedule, now time.Time) bool {
	day := dayShort(now.Weekday())
	for _, w := range s.Windows {
		if !containsDay(w.Days, day) {
			continue
		}
		if timeInWindow(now, w.Start, w.End) {
			return true
		}
	}
	return false
}

var weekdayNames = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

func dayShort(d time.Weekday) string { return weekdayNames[d] }

func containsDay(days []string, day string) bool {
	for _, d := range days {
		if strings.EqualFold(d, day) {
			return true
		}
	}
	return false
}

// timeInWindow reports whether `now` (as wall-clock HH:MM) is in
// [start, end). When end < start, the window wraps midnight, so an
// 23:00-02:00 spec is active from 23:00 today AND 00:00-02:00 today.
func timeInWindow(now time.Time, start, end string) bool {
	curMin := now.Hour()*60 + now.Minute()
	s, ok := parseHHMM(start)
	if !ok {
		return false
	}
	e, ok := parseHHMM(end)
	if !ok {
		return false
	}
	if s == e {
		return false
	}
	if s < e {
		return curMin >= s && curMin < e
	}
	// Wrap: 22:00-02:00 means [22:00, 24:00) OR [00:00, 02:00).
	return curMin >= s || curMin < e
}

func parseHHMM(s string) (int, bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Now returns the schedule-evaluation reference time. In production it's
// time.Now() in the node's local zone. When SKOED_TEST_MODE=1 AND
// SKOED_TEST_NOW is set to an RFC3339 timestamp, that value is returned
// instead — used by the acceptance tests to drive deterministic schedule
// evaluation without sleeping.
func Now() time.Time {
	if os.Getenv("SKOED_TEST_MODE") == "1" {
		if v := os.Getenv("SKOED_TEST_NOW"); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t.In(time.Local)
			}
		}
	}
	return time.Now()
}
