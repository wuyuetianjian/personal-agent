//go:build nativeprobe_sqlite_persistent

package rootlease

import (
	"net/url"
	"strings"
	"testing"
)

// Host fixture drive-absolute filenames only; same Path escaping strategy as
// the separately tested private factory. Never Opaque or raw DSN concatenation.
func persistentSQLURI(filename string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(filename, `\`, "/"), RawQuery: url.Values{"mode": {"rw"}, "vfs": {"agent-persistent-red-v1"}}.Encode()}).String()
}
func TestPersistentSQLURI(t *testing.T) {
	const filename = `C:\owned folder\literal%23#?数据库.db`
	got := persistentSQLURI(filename)
	u, e := url.Parse(got)
	if e != nil {
		t.Fatal(e)
	}
	if u.Scheme != "file" || u.Host != "" || u.Opaque != "" || u.Fragment != "" || u.Path != "/C:/owned folder/literal%23#?数据库.db" || len(u.Query()) != 2 || u.Query().Get("mode") != "rw" || u.Query().Get("vfs") != "agent-persistent-red-v1" {
		t.Fatalf("wrong Windows literal file URI: %q %#v", got, u)
	}
	if !strings.HasPrefix(got, "file:///C:/") {
		t.Fatalf("drive URI authority ambiguity: %s", got)
	}
	t.Log(got)
}
