package store

import "testing"

func TestWrongContentStreak(t *testing.T) {
	cases := []struct {
		ds   []string
		want int
	}{
		{nil, 0},
		{[]string{"synced", "rejected_wrong_content"}, 0},
		{[]string{"rejected_wrong_content", "rejected_wrong_content", "synced"}, 2},
		{[]string{"rejected_wrong_content", "timeout", "rejected_wrong_content"}, 2},
		{[]string{"kept_for_review", "rejected_wrong_content", "rejected_wrong_content"}, 3},
		{[]string{"rejected_wrong_content", "rejected_machine_translation", "rejected_wrong_content"}, 1},
	}
	for _, c := range cases {
		if got := wrongContentStreak(c.ds); got != c.want {
			t.Errorf("%v: got %d, want %d", c.ds, got, c.want)
		}
	}
}
