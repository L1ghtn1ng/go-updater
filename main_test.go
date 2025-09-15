package main

import (
	"strings"
	"testing"
)

func TestCleanVersionInput(t *testing.T) {
	tests := []struct {
		in  string
		out string
	}{
		{"go1.25.1", "go1.25.1"},
		{"1.25.1", "go1.25.1"},
		{"go1.25.1 time 2025-08-27T15:49:40Z", "go1.25.1"},
		{"go1.25.1\n time 2025-08-27T15:49:40Z", "go1.25.1"},
		{"   go1.22.6   ", "go1.22.6"},
		{"go1.24beta1", "go1.24beta1"},
		{"1.24beta1", "go1.24beta1"},
		{"go1.25.1rc1 extra", "go1.25.1rc1"},
		{"go1.25.1!", "go1.25.1"},
		{"go", "go"}, // minimal allowed form per sanitizer
	}

	for _, tt := range tests {
		got := cleanVersionInput(tt.in)
		if got != tt.out {
			t.Errorf("cleanVersionInput(%q) = %q; want %q", tt.in, got, tt.out)
		}
		if got != "" && !strings.HasPrefix(got, "go") {
			t.Errorf("cleanVersionInput(%q) must start with 'go' prefix; got %q", tt.in, got)
		}
	}
}

func TestParseGoVersionOutput(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"TypicalLinux", "go version go1.22.6 linux/amd64", "go1.22.6", false},
		{"Beta", "go version go1.24beta1 linux/amd64", "go1.24beta1", false},
		{"WindowsFormat", "go version go1.22.6 windows/amd64", "go1.22.6", false},
		{"FallbackScan", "random text go1.20.5 something", "go1.20.5", false},
		{"Garbage", "totally unrelated output", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGoVersionOutput(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil and %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseGoVersionOutput(%q) = %q; want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestContainsProfileLine(t *testing.T) {
	exact := "export PATH=$PATH:/usr/local/go/bin"

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"ExactLine", exact + "\n", true},
		{"WhitespaceVariation", "  \t" + exact + "  \n", true},
		{"AlternateOrder", "export PATH=/usr/local/go/bin:$PATH\n", true},
		{"MultipleLines", "# comment\nSOME=VAR\n" + exact + "\n", true},
		{"NotPresent", "# nothing relevant\nexport PATH=$PATH:/usr/local/bin\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsProfileLine(tt.content, exact)
			if got != tt.want {
				t.Errorf("containsProfileLine(%q, %q) = %v; want %v", tt.content, exact, got, tt.want)
			}
		})
	}
}
