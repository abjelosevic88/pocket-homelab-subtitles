package server

import (
	"net/http/httptest"
	"testing"
)

// subtitles:sync reaches the file lookup, nudge and undo — nothing else.
func TestLinkScopes(t *testing.T) {
	sync := []string{"subtitles:sync"}
	for _, ok := range []struct{ method, path string }{{"GET", "/api/v1/files/by-path"}, {"GET", "/api/v1/files/12"}, {"POST", "/api/v1/files/12/nudge"}, {"POST", "/api/v1/files/12/undo"}} {
		if !linkAllows(sync, httptest.NewRequest(ok.method, ok.path, nil)) {
			t.Errorf("%s %s should be allowed", ok.method, ok.path)
		}
	}
	for _, no := range []struct{ method, path string }{{"POST", "/api/v1/files/12/restore-original"}, {"POST", "/api/v1/sweep"}, {"GET", "/api/v1/settings"}, {"POST", "/api/v1/files/12/unpin"}} {
		if linkAllows(sync, httptest.NewRequest(no.method, no.path, nil)) {
			t.Errorf("%s %s must stay closed", no.method, no.path)
		}
	}
	if linkAllows(nil, httptest.NewRequest("POST", "/api/v1/files/12/nudge", nil)) {
		t.Error("a link without the scope must not nudge")
	}
}
