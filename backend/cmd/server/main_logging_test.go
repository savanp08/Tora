package main

import "testing"

func TestSanitizeLogLineRedactsSensitiveFragments(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "key-value identifiers",
			input:  "[ws] message edit failed room=abc123 user=u1 message=m9 err=timeout\n",
			expect: "[ws] message edit failed room=[redacted] user=[redacted] message=[redacted] err=timeout\n",
		},
		{
			name:   "email and ipv4 address",
			input:  "archive email sent to test@example.com from 10.0.0.4:8080\n",
			expect: "archive email sent to [redacted-email] from [redacted-ip]\n",
		},
		{
			name:   "identifier after room word",
			input:  "Could not preload current canvas snapshot for room abc123: boom\n",
			expect: "Could not preload current canvas snapshot for room [redacted] boom\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := sanitizeLogLine(tt.input)
			if actual != tt.expect {
				t.Fatalf("unexpected sanitized output\nwant: %q\ngot:  %q", tt.expect, actual)
			}
		})
	}
}
