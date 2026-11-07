package admin

import (
	"strings"
	"testing"

	"movex/internal/repo"
)

func TestGrowthSVG(t *testing.T) {
	points, area, maxValue := growthSVG([]repo.DayCount{{Day: "2026-11-04", Count: 0}, {Day: "2026-11-05", Count: 3}, {Day: "2026-11-06", Count: 1}})
	if maxValue != 3 || strings.Count(points, ",") != 3 || !strings.HasPrefix(area, points) || !strings.Contains(points, "8.0,") || !strings.Contains(points, "712.0,") {
		t.Fatalf("unexpected chart geometry: points=%q area=%q max=%d", points, area, maxValue)
	}
	p, a, m := growthSVG(nil)
	if p != "" || a != "" || m != 1 {
		t.Fatalf("empty chart: %q %q %d", p, a, m)
	}
	zeroPoints, _, zeroMax := growthSVG([]repo.DayCount{{Count: 0}, {Count: 0}})
	if zeroMax != 1 || strings.Contains(zeroPoints, "NaN") {
		t.Fatalf("zero-value chart must remain valid: %q max=%d", zeroPoints, zeroMax)
	}
}
