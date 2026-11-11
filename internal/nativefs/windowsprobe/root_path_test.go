package windowsprobe

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLocalRootPath(t *testing.T) {
	for _, tc := range []struct {
		path, drive string
		parts       []string
	}{
		{`C:\`, "C:", nil}, {`z:\`, "Z:", nil},
		{`c:\Users\me`, "C:", []string{"Users", "me"}},
		{`D:\Users\me\`, "D:", []string{"Users", "me"}},
		{`E:\.private\data\node_modules`, "E:", []string{".private", "data", "node_modules"}},
		{`F:\文件😀`, "F:", []string{"文件😀"}},
		{`G:\` + strings.Repeat("x", 255), "G:", []string{strings.Repeat("x", 255)}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			drive, parts, err := parseLocalRootPath(tc.path)
			if err != nil || drive != tc.drive || !reflect.DeepEqual(parts, tc.parts) {
				t.Fatalf("drive=%q parts=%q err=%v", drive, parts, err)
			}
		})
	}
}
func TestParseLocalRootPathRejectsAliases(t *testing.T) {
	bad := []string{"", `C:`, `C:relative`, `C:/Users`, `C:\Users/me`, `C :\Users`, `1:\Users`, `é:\Users`,
		`\\server\share`, `\\?\C:\Users`, `\\.\C:\Users`, `\Device\HarddiskVolume1\Users`,
		`C:\\`, `C:\Users\\me`, `C:\Users\\`, `C:\.`, `C:\..`, `C:\Users\..`, `C:\a:stream`,
		`C:\CON`, `C:\nul.txt`, `C:\COM¹.txt`, `C:\LPT³`, `C:\a.`, `C:\a `, `C:\a?`, `C:\a*`,
		"C:\\a\x00", "C:\\a\n", "C:\\a\x7f", "C:\\" + string([]byte{0xff}),
		`C:\` + strings.Repeat("x", 256), `C:\` + strings.Repeat("😀", 128),
		`C:\` + strings.TrimSuffix(strings.Repeat(`x\`, 33), `\`),
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			drive, parts, err := parseLocalRootPath(p)
			if err == nil || drive != "" || parts != nil {
				t.Fatalf("unsafe/partial result: drive=%q parts=%q err=%v", drive, parts, err)
			}
		})
	}
}
func TestRootPathDepthAndToolPolicy(t *testing.T) {
	path := `C:\` + strings.TrimSuffix(strings.Repeat(`x\`, 32), `\`)
	drive, parts, err := parseLocalRootPath(path)
	if err != nil || drive != "C:" || len(parts) != 32 {
		t.Fatalf("depth boundary: %q %d %v", drive, len(parts), err)
	}
	for _, name := range []string{".private", "data", "node_modules"} {
		if _, _, err := parseLocalRootPath(`C:\` + name); err != nil {
			t.Errorf("root policy rejects %q: %v", name, err)
		}
		if validateComponents([]string{name}) == nil {
			t.Errorf("tool policy weakened: %q", name)
		}
	}
}
func TestPhysicalVolumeRoot(t *testing.T) {
	const prefix = `\Device\HarddiskVolume`
	for _, s := range []string{prefix + "1", prefix + "123", prefix + strings.Repeat("1", 64-len(prefix))} {
		got, err := physicalVolumeRoot(s)
		if err != nil || got != s+`\` {
			t.Fatalf("%q: %q %v", s, got, err)
		}
	}
	for _, s := range []string{"", prefix, prefix + "1\\", prefix + "1\\Users", prefix + "1/", prefix + "1 ", prefix + "-1", prefix + "１", prefix + "1\x00", prefix + strings.Repeat("1", 65-len(prefix)),
		`\device\HarddiskVolume1`, `\Device\HarddiskVolumeShadowCopy1`, `\Device\HarddiskVolume1:stream`, `\??\C:\Users`, `\Device\LanmanRedirector\server\share`, `\Device\Mup\server\share`, `\\server\share`, `C:\Users`} {
		got, err := physicalVolumeRoot(s)
		if err == nil || got != "" {
			t.Errorf("accepted %q: %q %v", s, got, err)
		}
	}
}
