package srt

import "testing"

func TestShift(t *testing.T) {
	in := "\xEF\xBB\xBF1\n00:00:01,000 --> 00:00:02,500\nHello\n\n2\n01:59:59.900 --> 02:00:00,100\nBye\n"
	out, n := Shift([]byte(in), 250)
	want := "1\n00:00:01,250 --> 00:00:02,750\nHello\n\n2\n02:00:00,150 --> 02:00:00,350\nBye\n"
	if n != 2 || string(out) != want {
		t.Fatalf("n=%d\n%s", n, out)
	}
	back, _ := Shift(out, -250)
	if string(back) != "1\n00:00:01,000 --> 00:00:02,500\nHello\n\n2\n01:59:59,900 --> 02:00:00,100\nBye\n" {
		t.Fatalf("round trip:\n%s", back)
	}
	clamped, _ := Shift([]byte("00:00:00,100 --> 00:00:01,000"), -500)
	if string(clamped) != "00:00:00,000 --> 00:00:00,500" {
		t.Fatal(string(clamped))
	}
}

func TestCyrillic(t *testing.T) {
	if !HasCyrillic([]byte("Здраво")) || HasCyrillic([]byte("Zdravo, čćžšđ")) {
		t.Fatal("cyrillic detection")
	}
}
