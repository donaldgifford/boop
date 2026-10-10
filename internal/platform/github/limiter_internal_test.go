package github

import (
	"testing"

	"golang.org/x/time/rate"
)

func TestWithDiscoveredLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		limit     int
		wantRate  rate.Limit
		wantBurst int
	}{
		{name: "no reading keeps default", limit: 0, wantRate: defaultRateLimit, wantBurst: defaultRateBurst},
		{name: "negative keeps default", limit: -1, wantRate: defaultRateLimit, wantBurst: defaultRateBurst},
		{name: "5000 per hour", limit: 5000, wantRate: rate.Limit(5000.0 / 3600), wantBurst: 10},
		{name: "12500 per hour", limit: 12500, wantRate: rate.Limit(12500.0 / 3600), wantBurst: 10},
		{name: "enterprise cloud 15000", limit: 15000, wantRate: rate.Limit(15000.0 / 3600), wantBurst: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewWithToken(TokenAuth{Token: "t"}, WithDiscoveredLimit(tt.limit))
			if err != nil {
				t.Fatalf("NewWithToken: %v", err)
			}
			if got := c.limiter.Limit(); got != tt.wantRate {
				t.Errorf("WithDiscoveredLimit(%d) rate = %v, want %v", tt.limit, got, tt.wantRate)
			}
			if got := c.limiter.Burst(); got != tt.wantBurst {
				t.Errorf("WithDiscoveredLimit(%d) burst = %d, want %d", tt.limit, got, tt.wantBurst)
			}
		})
	}
}
