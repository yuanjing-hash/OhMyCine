package directory

import "testing"

func TestDecodeMountPathEscapes(t *testing.T) {
	input := `/media/My\040Library/tab\011name/line\012name/slash\134name`
	want := "/media/My Library/tab\tname/line\nname/slash\\name"
	if got := decodeMountPath(input); got != want {
		t.Fatalf("decoded mount path=%q want=%q", got, want)
	}
}
