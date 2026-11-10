package api

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestStaticAssetsOnlyAfterLoopbackAndOriginChecks(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	s.options.WebAssets = fstest.MapFS{"index.html": {Data: []byte("index")}, "assets/main.js": {Data: []byte("script")}}
	for _, tc := range []struct {
		path, host, origin string
		want               int
	}{{"/", "127.0.0.1:7319", "", 200}, {"/assets/main.js", "127.0.0.1:7319", "", 200}, {"/missing", "127.0.0.1:7319", "", 404}, {"/api/v1/status", "127.0.0.1:7319", "", 401}, {"/", "evil.test", "", 403}, {"/", "127.0.0.1:7319", "http://evil.test", 403}} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7319"+tc.path, nil)
		r.RemoteAddr = "127.0.0.1:1"
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d", tc.path, w.Code, tc.want)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Error("missing CSP")
		}
	}
}
