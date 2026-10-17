package platform

import "testing"

func TestExecutableNameForOS(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{goos: "windows", want: "ffmpeg.exe"},
		{goos: "linux", want: "ffmpeg"},
		{goos: "darwin", want: "ffmpeg"},
	}

	for _, testCase := range cases {
		if got := executableNameForOS("ffmpeg", testCase.goos); got != testCase.want {
			t.Errorf("executableNameForOS(ffmpeg, %q) = %q, want %q", testCase.goos, got, testCase.want)
		}
	}
}
