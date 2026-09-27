package pathutil

import (
	"testing"
)

func TestSplitPath(t *testing.T) {
	tests := []struct {
		input      string
		wantParent string
		wantName   string
	}{
		{"/", "", ""},
		{"", "", ""},
		{"/foo", "/", "foo"},
		{"/foo/bar", "/foo", "bar"},
		{"/foo/bar/baz.txt", "/foo/bar", "baz.txt"},
		{"foo", "/", "foo"},
		{"/books/scifi/chapter_01_story...part1.fb2", "/books/scifi", "chapter_01_story...part1.fb2"},
		{"/documents/network/manual_v2..final.djvu", "/documents/network", "manual_v2..final.djvu"},
	}

	for _, tt := range tests {
		parent, name := SplitPath(tt.input)
		if parent != tt.wantParent || name != tt.wantName {
			t.Errorf("SplitPath(%q) = (%q, %q), want (%q, %q)", tt.input, parent, name, tt.wantParent, tt.wantName)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", "/"},
		{"foo", "/foo"},
		{"/foo/", "/foo"},
		{"/foo/bar/", "/foo/bar"},
		{"/foo//bar/", "/foo/bar"},
		{"/foo/../bar", "/bar"},
		{"/./foo", "/foo"},
	}

	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		got := FormatBytes(tt.input)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateFileID(t *testing.T) {
	id1 := GenerateFileID(1)
	id2 := GenerateFileID(1)
	if id1 == id2 {
		t.Errorf("GenerateFileID should produce unique values, got collision: %s", id1)
	}
}

func TestFormatContentDisposition(t *testing.T) {
	tests := []struct {
		dispType string
		filename string
		expected string
	}{
		{"attachment", "simple.pdf", `attachment; filename=simple.pdf`},
		{"attachment", `file "with" quotes.txt`, `attachment; filename="file \"with\" quotes.txt"`},
		{"attachment", "line\nbreak\rtest.txt", `attachment; filename=linebreaktest.txt`},
		{"inline", "", `inline; filename=download`},
	}

	for _, tt := range tests {
		got := FormatContentDisposition(tt.dispType, tt.filename)
		if got != tt.expected {
			t.Errorf("FormatContentDisposition(%q, %q) = %q, want %q", tt.dispType, tt.filename, got, tt.expected)
		}
	}
}

func TestValidatePathLength(t *testing.T) {
	shortPath := "/valid/short/path.txt"
	if err := ValidatePathLength(shortPath); err != nil {
		t.Errorf("expected valid path length, got error: %v", err)
	}

	exactPath := "/" + string(make([]byte, MaxPathLength-1))
	if err := ValidatePathLength(exactPath); err != nil {
		t.Errorf("expected path of exact MaxPathLength to be valid, got error: %v", err)
	}

	tooLongPath := "/" + string(make([]byte, MaxPathLength))
	if err := ValidatePathLength(tooLongPath); err == nil {
		t.Errorf("expected error for path exceeding MaxPathLength, got nil")
	}
}
