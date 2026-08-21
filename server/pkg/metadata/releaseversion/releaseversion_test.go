package releaseversion

import "testing"

func TestParseExtractsOrderedReleaseVersionWithoutNoise(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD.mkv", "2160p UHD BluRay"},
		{"Blade.Runner.1982.Final.Cut.2160p.UHD.BluRay.REMUX.DV.HDR10.TrueHD.Atmos-GROUP.mkv", "Final Cut 2160p UHD BluRay REMUX HDR10 Dolby Vision"},
		{"Aliens.1986.Extended.Directors.Cut.1080p.BluRay.REMUX-GROUP.mkv", "Extended Director's Cut 1080p BluRay REMUX"},
		{"Movie.2024.1080p.WEB-DL.HDR10Plus.DoVi.DDP5.1.H.265-GROUP.mkv", "1080p WEB-DL HDR10+ Dolby Vision"},
		{"电影.2024.导演剪辑版.2160p.WEB-DL.HDR10.mkv", "Director's Cut 2160p WEB-DL HDR10"},
		{"Ordinary.Movie.2024.x265.DTS-GROUP.mkv", ""},
		{"A.Movie.About.IMAX.2024.mkv", "IMAX"},
		{"DVD.Collection.2024.DVDRip.mkv", "DVDRip"},
	}
	for _, test := range tests {
		if got := Parse(test.name); got != test.want {
			t.Errorf("Parse(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}
