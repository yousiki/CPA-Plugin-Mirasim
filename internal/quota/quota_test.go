package quota

import (
	"math"
	"testing"
)

func TestUtilizationPercent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want float64
	}{
		{name: "absent header", raw: "", want: -1},
		{name: "unparseable header", raw: "n/a", want: -1},
		{name: "zero", raw: "0", want: 0},
		// The value observed on the live relay.
		{name: "fraction is scaled to percent", raw: "0.22689583333333332", want: 22.6895833333},
		{name: "one is full", raw: "1", want: 100},
		// Above 1 can only mean the gateway switched to percentages; scaling again would
		// report 100% for a barely-used account.
		{name: "already a percentage is passed through", raw: "42.5", want: 42.5},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := utilizationPercent(test.raw)
			if math.Abs(got-test.want) > 1e-9 {
				t.Errorf("utilizationPercent(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestHeaderInt(t *testing.T) {
	if got := headerInt("1786673322"); got != 1786673322 {
		t.Errorf("headerInt = %d, want 1786673322", got)
	}
	// A missing reset header means "no reset time", which the console renders as absent.
	if got := headerInt(""); got != 0 {
		t.Errorf("headerInt(\"\") = %d, want 0", got)
	}
}

func TestCacheIsKeyedCaseInsensitively(t *testing.T) {
	t.Cleanup(func() { Forget("Someone@Example.com") })

	Store("Someone@Example.com", Snapshot{Utilization7d: 12})
	got, ok := Lookup("someone@example.com  ")
	if !ok {
		t.Fatal("Lookup missed a reading stored under a differently-cased email")
	}
	if got.Utilization7d != 12 {
		t.Errorf("Utilization7d = %v, want 12", got.Utilization7d)
	}

	Forget("SOMEONE@EXAMPLE.COM")
	if _, ok = Lookup("someone@example.com"); ok {
		t.Error("Forget did not drop the reading")
	}
}

// An empty email is not a cache key: storing under it would make every unidentifiable
// account share one reading.
func TestStoreRejectsAnEmptyEmail(t *testing.T) {
	Store("   ", Snapshot{Utilization7d: 99})
	if _, ok := Lookup(""); ok {
		t.Error("an empty email was cached")
	}
}

// The relay's error code is the whole diagnostic value of a 401; flattening it to the
// status code is what made a rejected credential indistinguishable from a rejected ticket.
func TestRelayErrorDetail(t *testing.T) {
	cases := map[string]string{
		`{"error":{"code":"token_invalid","message":"invalid user token","type":"authentication_error"},"type":"error"}`: "token_invalid",
		`{"error":{"code":"token_missing","message":"missing user token"},"type":"error"}`:                               "token_missing",
		`{"error":{"message":"device session expired"}}`:                                                                 "device session expired",
		`not json at all`: "",
		`{}`:              "",
	}
	for body, want := range cases {
		if got := relayErrorDetail([]byte(body)); got != want {
			t.Fatalf("relayErrorDetail(%q) = %q, want %q", body, got, want)
		}
	}
}
