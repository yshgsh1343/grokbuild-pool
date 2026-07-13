package lease

import "testing"

func TestDecayFailureCount(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 0},
		{1, 0},
		{2, 0},
		{3, 1},
		{5, 2},
		{10, 4},
		{11, 5},
	}
	for _, tc := range cases {
		if got := decayFailureCount(tc.in); got != tc.want {
			t.Fatalf("decay(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}
